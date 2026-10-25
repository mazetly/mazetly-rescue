"""Seeds or checks an Uptime Kuma instance over socket.io (it has no REST API).

    seed.py <url> <password> seed|check
"""
import sys
import time

import socketio

url, password, mode = sys.argv[1:4]


def session():
    """Connects and logs in, creating the account first when seeding.

    Kuma answers HTTP well before its socket handlers reply: retry until they do.
    """
    for _ in range(24):
        client = socketio.Client()
        client.connect(url, transports=["websocket"])
        try:
            if mode == "seed":
                client.call("setup", ("rescue", password), timeout=10)
            login = client.call("login", {"username": "rescue", "password": password, "token": ""}, timeout=10)
            assert login["ok"], login
            return client
        except socketio.exceptions.TimeoutError:
            client.disconnect()
            time.sleep(5)
    sys.exit("Uptime Kuma never answered")


sio = session()
if mode == "seed":
    added = sio.call("add", {
        "type": "http", "name": "Rescue CI", "url": "https://example.org",
        "interval": 3600, "retryInterval": 60, "maxretries": 0, "method": "GET",
        "accepted_statuscodes": ["200-299"], "notificationIDList": {}, "conditions": [],
        "kafkaProducerBrokers": [], "kafkaProducerSaslOptions": {}, "rabbitmqNodes": [],
    }, timeout=30)
    assert added["ok"], added
monitor = sio.call("getMonitor", 1, timeout=30)
assert monitor["ok"] and monitor["monitor"]["name"] == "Rescue CI", monitor
sio.disconnect()
