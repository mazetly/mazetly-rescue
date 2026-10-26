"""Seeds or checks a Vaultwarden instance the way a Bitwarden client does: the
vault is encrypted here, with keys derived from the master password, and the
server only ever sees ciphertext.

    seed.py <url> <admin token> <password> <expect.json> seed|check

seed invites an account from the admin page, registers it, then stores a login
and an attachment; check logs in again and decrypts both. expect.json holds
what check must find: the plaintext values, never the keys.
"""
import base64
import hashlib
import hmac
import json
import os
import sys
import uuid

import requests
from cryptography.hazmat.primitives import hashes, padding, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
from cryptography.hazmat.primitives.kdf.hkdf import HKDFExpand

url, admin_token, password, expect_path, mode = sys.argv[1:6]
EMAIL = "rescue@example.org"
ITERATIONS = 600_000
b64 = lambda b: base64.b64encode(b).decode()
# Vaultwarden logs an error for clients that do not say which version they are.
CLIENT = {"Bitwarden-Client-Name": "cli", "Bitwarden-Client-Version": "2026.4.0"}


def stretch(master_key):
    """The 64-byte key (encryption + MAC) that protects the user key."""
    expand = lambda info: HKDFExpand(hashes.SHA256(), 32, info).derive(master_key)
    return expand(b"enc") + expand(b"mac")


def encrypt(key, data):
    """AES-256-CBC with HMAC-SHA256, as raw iv, ciphertext and MAC."""
    iv = os.urandom(16)
    padder = padding.PKCS7(128).padder()
    enc = Cipher(algorithms.AES(key[:32]), modes.CBC(iv)).encryptor()
    ct = enc.update(padder.update(data) + padder.finalize()) + enc.finalize()
    return iv, ct, hmac.new(key[32:], iv + ct, hashlib.sha256).digest()


def decrypt(key, iv, ct, mac):
    assert hmac.compare_digest(mac, hmac.new(key[32:], iv + ct, hashlib.sha256).digest()), "bad MAC"
    dec = Cipher(algorithms.AES(key[:32]), modes.CBC(iv)).decryptor()
    unpadder = padding.PKCS7(128).unpadder()
    return unpadder.update(dec.update(ct) + dec.finalize()) + unpadder.finalize()


def enc_string(key, data):
    iv, ct, mac = encrypt(key, data)
    return f"2.{b64(iv)}|{b64(ct)}|{b64(mac)}"


def dec_string(key, s):
    kind, rest = s.split(".", 1)
    assert kind == "2", s
    return decrypt(key, *map(base64.b64decode, rest.split("|")))


def master_hash():
    master_key = hashlib.pbkdf2_hmac("sha256", password.encode(), EMAIL.encode(), ITERATIONS)
    return master_key, b64(hashlib.pbkdf2_hmac("sha256", master_key, password.encode(), 1))


def login():
    """Password grant, as a CLI client. Returns the API session and the user key."""
    master_key, hashed = master_hash()
    token = requests.post(f"{url}/identity/connect/token", headers=CLIENT, data={
        "grant_type": "password", "username": EMAIL, "password": hashed,
        "scope": "api offline_access", "client_id": "cli",
        "deviceType": "8", "deviceIdentifier": str(uuid.uuid4()), "deviceName": "rescue-ci",
    }, timeout=30)
    assert token.ok, token.text
    body = token.json()
    api = requests.Session()
    api.headers.update(CLIENT)
    api.headers["Authorization"] = f"Bearer {body['access_token']}"
    return api, dec_string(stretch(master_key), body["Key"])


def seed():
    admin = requests.Session()
    r = admin.post(f"{url}/admin", data={"token": admin_token}, allow_redirects=False, timeout=30)
    assert r.status_code in (200, 303) and admin.cookies, f"admin login: {r.status_code}"
    r = admin.post(f"{url}/admin/invite", json={"email": EMAIL}, timeout=30)
    assert r.ok, f"invite: {r.status_code} {r.text}"

    # Without SMTP, an invited address registers directly.
    master_key, hashed = master_hash()
    user_key = os.urandom(64)
    private = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    r = requests.post(f"{url}/identity/accounts/register", headers=CLIENT, json={
        "email": EMAIL, "name": "Rescue CI", "masterPasswordHash": hashed,
        "key": enc_string(stretch(master_key), user_key),
        "kdf": 0, "kdfIterations": ITERATIONS,
        "keys": {
            "publicKey": b64(private.public_key().public_bytes(
                serialization.Encoding.DER, serialization.PublicFormat.SubjectPublicKeyInfo)),
            "encryptedPrivateKey": enc_string(user_key, private.private_bytes(
                serialization.Encoding.DER, serialization.PrivateFormat.PKCS8, serialization.NoEncryption())),
        },
    }, timeout=30)
    assert r.ok, f"register: {r.status_code} {r.text}"

    api, user_key = login()
    secret = os.urandom(12).hex()
    r = api.post(f"{url}/api/ciphers", json={
        "type": 1, "name": enc_string(user_key, b"Rescue CI"), "notes": None, "favorite": False,
        "login": {"username": enc_string(user_key, b"rescue"), "password": enc_string(user_key, secret.encode())},
    }, timeout=30)
    assert r.ok, f"cipher: {r.status_code} {r.text}"
    cipher_id = r.json()["id"]

    # An attachment is a file under attachments/, outside the database: it
    # proves the backup takes the data folder, not only db.sqlite3.
    content = os.urandom(4096)
    att_key = os.urandom(64)
    iv, ct, mac = encrypt(att_key, content)
    blob = b"\x02" + iv + mac + ct
    r = api.post(f"{url}/api/ciphers/{cipher_id}/attachment/v2", json={
        "key": enc_string(user_key, att_key), "fileName": enc_string(user_key, b"rescue.bin"),
        "fileSize": len(blob), "adminRequest": False,
    }, timeout=30)
    assert r.ok, f"attachment: {r.status_code} {r.text}"
    att_id = r.json()["attachmentId"]
    r = api.post(f"{url}/api/ciphers/{cipher_id}/attachment/{att_id}",
                 files={"data": ("rescue.bin", blob, "application/octet-stream")}, timeout=30)
    assert r.ok, f"upload: {r.status_code} {r.text}"

    with open(expect_path, "w") as f:
        json.dump({"cipher": cipher_id, "password": secret,
                   "attachment": hashlib.sha256(content).hexdigest()}, f)


def check():
    with open(expect_path) as f:
        expect = json.load(f)
    api, user_key = login()
    r = api.get(f"{url}/api/sync", timeout=30)
    assert r.ok, f"sync: {r.status_code} {r.text}"
    cipher = next(c for c in r.json()["ciphers"] if c["id"] == expect["cipher"])
    assert dec_string(user_key, cipher["login"]["password"]).decode() == expect["password"], "password differs"

    att = cipher["attachments"][0]
    r = api.get(f"{url}/api/ciphers/{cipher['id']}/attachment/{att['id']}", timeout=30)
    assert r.ok, f"attachment: {r.status_code} {r.text}"
    blob = requests.get(r.json()["url"], timeout=30).content
    assert blob[0] == 2, "not an encrypted attachment"
    att_key = dec_string(user_key, att["key"])
    content = decrypt(att_key, blob[1:17], blob[49:], blob[17:49])
    assert hashlib.sha256(content).hexdigest() == expect["attachment"], "attachment differs"


seed() if mode == "seed" else check()
