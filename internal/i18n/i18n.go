// Package i18n holds the tool's messages in English and French, chosen from
// the system language.
package i18n

import (
	"fmt"
	"os"
	"strings"
)

var messages = map[string][2]string{
	// key: {English, French}
	"backup.hooks":     {"Running %s hooks", "Commandes %s"},
	"backup.export":    {"Exporting database %s (%s)", "Export de la base %s (%s)"},
	"backup.restic":    {"Sending the kit to the repository", "Envoi du kit vers le dépôt"},
	"backup.done":      {"Backup of %s %s done.", "Sauvegarde de %s %s terminée."},
	"restore.snapshot": {"Restoring the snapshot of %s (%s, %s)", "Restauration de l'instantané du %s (%s, %s)"},
	"restore.verify":   {"Checking the database exports", "Vérification des exports de base"},
	"restore.pin":      {"Pinning images to the backed-up versions", "Épinglage des images sur les versions sauvegardées"},
	"restore.import":   {"Importing database %s (%s)", "Import de la base %s (%s)"},
	"restore.start":    {"Starting the application", "Démarrage de l'application"},
	"restore.done":     {"%s %s is running again in %s.", "%s %s tourne à nouveau dans %s."},
	"restore.next":     {"Next: read NOTES.md (new domain, upgrades), point your domain at this server and put a TLS proxy in front of it.", "Ensuite : lisez NOTES.md (nouveau domaine, mises à jour), faites pointer votre domaine vers ce serveur et placez un proxy TLS devant."},
	"restore.version":  {"the restored application runs version %q, the backup recorded %q", "l'application restaurée tourne en version %q, la sauvegarde indiquait %q"},
	"restore.notempty": {"%s exists and is not empty", "%s existe et n'est pas vide"},
	"check.latest":     {"Latest backup: %s (%s ago), %s %s, from %s", "Dernière sauvegarde : %s (il y a %s), %s %s, depuis %s"},
	"check.db":         {"Database export %s: readable, checksum OK", "Export de base %s : lisible, somme de contrôle correcte"},
	"check.ok":         {"Your key opens the repository and your latest backup is readable.", "Votre clé ouvre le dépôt et votre dernière sauvegarde est lisible."},
	"check.old":        {"The latest backup is older than %s.", "La dernière sauvegarde a plus de %s."},
	"check.none":       {"The repository holds no backup yet.", "Le dépôt ne contient encore aucune sauvegarde."},
	"check.mismatch":   {"Database export %s does not match its checksum", "L'export de base %s ne correspond pas à sa somme de contrôle"},
	"init.done":        {"Repository created with your key.", "Dépôt créé avec votre clé."},
	"init.customer":    {"Customer restore key (shown once, give it to the customer):", "Clé de restauration du client (affichée une seule fois, à remettre au client) :"},
	"password.prompt":  {"Repository password: ", "Mot de passe du dépôt : "},
	"password.missing": {"no repository password: set RESTIC_PASSWORD or RESTIC_PASSWORD_FILE, or use --password-file", "aucun mot de passe de dépôt : définissez RESTIC_PASSWORD ou RESTIC_PASSWORD_FILE, ou utilisez --password-file"},
	"repo.missing":     {"no repository: set RESTIC_REPOSITORY or use --repo", "aucun dépôt : définissez RESTIC_REPOSITORY ou utilisez --repo"},
	"error":            {"error: %v", "erreur : %v"},
	"backup.envmode":   {".env was readable by others (%v): now 0600", ".env était lisible par d'autres (%v) : passé en 0600"},
	"restore.review":   {"What this kit will run", "Ce que ce kit va lancer"},
	"restore.hook":     {"after restore, in", "après restauration, dans"},
	"restore.unsafe":   {"This kit reaches beyond its directory:", "Ce kit sort de son dossier :"},
	"restore.refused":  {"refused: a restored kit must stay within its directory (--allow-unsafe if you trust it)", "refusé : un kit restauré doit rester dans son dossier (--allow-unsafe si vous lui faites confiance)"},
	"restore.needyes":  {"no terminal to confirm on: review the kit, then run again with --yes", "pas de terminal pour confirmer : relisez le kit, puis relancez avec --yes"},
	"restore.confirm":  {"Start it? [y/N] ", "Le démarrer ? [o/N] "},
	"restore.aborted":  {"stopped before starting anything; the kit stays in place", "arrêt avant tout démarrage ; le kit reste en place"},
	"copy.init":        {"Creating the destination repository", "Création du dépôt de destination"},
	"copy.copy":        {"Copying the snapshots it lacks", "Copie des instantanés manquants"},
	"copy.done":        {"The destination holds the backup of %s.", "La destination contient la sauvegarde du %s."},
	"verify.cleanup":   {"Removing the test restore", "Suppression de la restauration de test"},
	"verify.ok":        {"The latest backup restores and starts: verified.", "La dernière sauvegarde se restaure et démarre : vérifié."},
}

var french = isFrench()

func isFrench() bool {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(name); v != "" {
			return strings.HasPrefix(v, "fr")
		}
	}
	return false
}

// T returns the message for key in the system language.
func T(key string, args ...any) string {
	m, ok := messages[key]
	if !ok {
		panic("i18n: unknown message " + key)
	}
	format := m[0]
	if french {
		format = m[1]
	}
	return fmt.Sprintf(format, args...)
}
