# skript-docs

Syntaxes des add-ons [Skript](https://github.com/SkriptLang/Skript), **générées automatiquement à
partir de leurs releases GitHub** et publiées ici en JSON, avec l'historique de chaque génération.

Les syntaxes ne sont pas lues dans le code source mais **sur un vrai serveur** : le jar de la
dernière release d'un add-on est lancé dans un serveur Paper + Skript jetable, sans réseau, avec un
plugin d'export qui écrit ce que l'add-on a réellement enregistré. Les motifs construits à
l'exécution (helpers, constantes, fichiers de langue, enregistrement conditionnel…) sont donc exacts.

## Ajouter un add-on

Une ligne dans [`addons.json`](addons.json), par pull request :

```json
{ "slug": "mongosk", "name": "MongoSK", "repo": "Romitou/MongoSK" }
```

| Champ | |
|---|---|
| `slug` | Dossier sous `addons/` et clé des consommateurs : minuscules, chiffres et tirets, unique. |
| `name` | Nom affiché. |
| `repo` | Dépôt GitHub public, `owner/name`, qui publie le jar dans ses releases. |
| `asset` | Facultatif : expression régulière choisissant le jar quand une release en contient plusieurs. Par défaut, tous les `.jar` sauf `-sources`/`-javadoc`, et le plus gros (souvent la version « shaded ») s'il en reste plusieurs. |
| `prereleases` | Facultatif, `false` par défaut : documenter aussi les pré-versions. |

La CI vérifie le fichier et l'existence des dépôts. Si l'add-on dépend d'autres plugins, les ajouter
à [`deps.json`](#dépendances-depsjson). Retirer une ligne arrête les générations ; l'historique reste
(marqué `tracked: false`), sauf à supprimer aussi `addons/<slug>/`.

## Les données

```
index.json                                 une ligne par add-on : dernière génération réussie, dernière génération
addons/<slug>/index.json                   l'historique des générations de l'add-on
addons/<slug>/<génération>.json            les syntaxes produites par une génération réussie
addons/<slug>/<génération>.log             fin du journal du serveur d'une génération en échec
addons.json, deps.json                     configuration (voir plus haut et plus bas)
status.json                                mois du dernier passage
```

Une **génération** est une exécution de l'extraction sur une release ; elle n'est jamais modifiée
ni remplacée. Son identifiant est sa date et son heure (UTC) suivies du tag :
`20261119-165207-v3.0.0`. Pour suivre les changements, lire `index.json` puis `latest.file` des
add-ons dont `latest.generation` a changé ; pour du contenu stable, lire
`raw.githubusercontent.com` à un commit plutôt que sur `main`.

### `index.json`

```json
{
  "formatVersion": 1,
  "addons": [{
    "slug": "mongosk", "name": "MongoSK", "repo": "Romitou/MongoSK", "tracked": true,
    "latest": { "generation": "20261119-165207-v3.0.0", "release": "v3.0.0",
                "file": "addons/mongosk/20261119-165207-v3.0.0.json", "syntaxCount": 53,
                "jarSha256": "001dfe68…", "generatedAt": "2026-11-19T16:52:07Z" },
    "last": { "generation": "20261119-165207-v3.0.0", "release": "v3.0.0", "status": "succeeded" }
  }]
}
```

`latest` est la génération réussie la plus récente (`null` s'il n'y en a pas), `last` la plus
récente, avec `errorCode` si elle a échoué.

### `addons/<slug>/index.json`

`slug`, `name`, `repo`, `latest` (identifiant de la dernière génération réussie) et `generations`,
de la plus récente à la plus ancienne. Pour chacune : `id`, `release` (`tag`, `name`, `url`,
`prerelease`, `publishedAt`), `assetId`, `assetName`, `jarUrl`, `jarSha256`, `reason` (pourquoi
elle a eu lieu), `status` (`succeeded` | `failed`), `errorCode`, `errorMessage`, `file`, `log`,
`syntaxCount`, `plugin` (`name`, `version`, `apiVersion` du `plugin.yml`), `image`, `minecraft`,
`skript`, `dependencies` (plugins installés à côté), `warnings`, `generatedAt`.

### `addons/<slug>/<génération>.json`

```json
{
  "format": "skmc-syntax-export", "formatVersion": 1, "language": "en",
  "source": { "plugin": "MongoSK", "version": "3.0.0", "skriptVersion": "2.17.1", "minecraftVersion": "26.3" },
  "events":      [{ "id": "mongo_command_failed", "name": "On Mongo command failed", "documented": false,
                    "description": [], "patterns": ["mongo[(db|sk)] command fail[ed]"], "examples": [],
                    "since": [], "requiredPlugins": [], "cancellable": false }],
  "conditions":  [], "effects": [],
  "expressions": [{ "id": "ExprLastMongoError", "name": "Last Mongo error", "documented": true,
                    "description": ["Returns the error message of the last Mongo request…"],
                    "patterns": ["[the] last mongo[(sk|db)] error"], "examples": ["…"], "since": ["3.0.0"],
                    "requiredPlugins": [], "returnType": "Text" }],
  "sections": [], "structures": [], "types": [], "functions": [],
  "warnings": ["…"],
  "stats": { "total": 53, "ambiguous": 0, "undocumented": 3 }
}
```

Les `id` sont ceux de la documentation de Skript (`DocumentationIdProvider` : nom de classe ou
`@DocumentationId`, id des événements), comme dans le JSON de Skript et du Skript Hub Docs Tool. Un
élément sans `@Name` a `documented: false` et un titre tiré de sa classe (`ExprMongoKeys` →
« Mongo Keys »). `stats.ambiguous` compte les éléments attribués sans autre preuve (voir
[Attribution](#attribution-des-syntaxes)).

## Fonctionnement

```
toutes les 6 h ─► plan : pour chaque add-on d'addons.json, la dernière release du dépôt
                         (hors brouillons, hors pré-versions sauf demande) et son jar ;
                         une génération si quelque chose a changé depuis la précédente
               ─► extract (une tâche par add-on, aucune permission d'écriture, aucun secret) :
                         télécharge le jar, lit plugin.yml, installe les dépendances de deps.json,
                         lance le conteneur d'extraction → résultat en artefact
               ─► publish (contents: write, n'exécute aucun code d'add-on) :
                         ne retient que les résultats planifiés, les vérifie et les nettoie,
                         ajoute la génération à l'historique, réécrit index.json, committe
```

Une nouvelle génération a lieu quand :

- l'add-on n'en a encore aucune, ou une **nouvelle release** est sortie ;
- le jar de la release a été remplacé ;
- les images d'extraction ont changé (`extractor/images.json` : nouvelle version de Skript ou de
  Minecraft), y compris pour une release déjà générée avec succès ;
- la dernière génération a échoué et `deps.json` a changé (une dépendance manquante a été ajoutée) ;
- elle est demandée : onglet *Actions* → *Extract syntaxes* → *Run workflow*, avec le `slug` d'un
  add-on et `force`.

Rien d'autre : une génération n'est jamais refaite pour rien. Chaque passage en lance au plus 20 ;
la suite attend le suivant.

| Dossier | Rôle |
|---|---|
| `scanner/` | Programme Go : `validate`, `plan`, `extract`, `publish`. |
| `extractor/exporter/` | Plugin Bukkit chargé dans le serveur : exporte les syntaxes de l'add-on cible puis arrête le serveur. |
| `extractor/image/` | Image Docker : Paper + Skript patchés à la construction, référence de Skript enregistrée, démarrage hors ligne. |
| `extractor/images.json` | Les images (Minecraft, Java, Skript), lues par les workflows et le scanner. |
| `extractor/test-addon/`, `extractor/test/run.sh` | Add-on minimal et test de bout en bout d'une image. |
| `.github/workflows/extract.yml` | Le cycle ci-dessus. |
| `.github/workflows/images.yml` | Construit, teste et publie les images sur GHCR. |
| `.github/workflows/ci.yml` | Tests du scanner, vérification de la configuration. |

### Attribution des syntaxes

Skript garde toutes les syntaxes dans un seul registre (le `JSONGenerator` de Skript exporte donc
tout, Skript compris). L'exporter :

1. retire ce que Skript enregistre seul, noté une fois à la construction de l'image
   (`/opt/skmc/baseline.json`), avec les mêmes Paper et Skript ;
2. attribue le reste avec l'origine que Skript enregistre et le plugin dont le class loader fournit
   les classes de l'élément. Ce qui vient d'un autre plugin (une dépendance elle-même add-on) est
   écarté, comme les hooks de Skript (Vault, WorldGuard…).

Seul cas sans preuve : un élément bâti sur une classe générique de Skript (`SimpleEvent` avec un
événement Bukkit/Paper). Il est gardé et compté dans `stats.ambiguous`.

### Choix de l'image

Une image par entrée d'`extractor/images.json`. Le scanner prend la plus ancienne qui satisfait
l'`api-version` de l'add-on (Paper garde les vieux plugins mais retire des API avec le temps), puis
la suivante si Paper refuse le plugin ou s'il ne s'active pas.

## Sécurité

Un jar d'add-on est du code arbitraire.

- **Le conteneur d'extraction** : aucun réseau (`--network none`), racine en lecture seule (tmpfs
  pour le serveur), utilisateur non root, aucune capability, `no-new-privileges`, mémoire, swap, CPU,
  processus et durée bornés.
- **La tâche `extract`** n'a ni droit d'écriture ni secret (`contents: read`, `packages: read` pour
  les images, avec une connexion à GHCR fermée avant l'extraction) : même une sortie de conteneur ne
  donne qu'une VM jetable.
- **La tâche `publish`**, seule à pouvoir committer, n'exécute aucun code d'add-on et traite les
  résultats comme non fiables : l'identité de ce qui est publié (add-on, release, jar) vient du plan,
  calculé avant toute extraction ; un résultat qui ne correspond à aucune génération planifiée est
  ignoré ; les exports sont vérifiés (format, 3 000 syntaxes au plus, champs nettoyés et bornés,
  champs inconnus retirés).
- **Téléchargements** : seulement les fichiers des releases du dépôt déclaré
  (`https://github.com/<repo>/releases/download/…`), 64 Mo au plus. Les dépendances viennent de
  `deps.json`, avec leur sha256 vérifié.
- **Ce qui est suivi** passe par une pull request relue ; un consommateur (Skript-MC par exemple)
  peut en plus faire relire chaque génération avant de la publier.

## Dépendances (`deps.json`)

Les conteneurs n'ayant pas de réseau, les plugins dont un add-on dépend doivent être listés ici. Le
dépôt ne contient pas leurs jars (leurs licences ne permettent pas toujours de les redistribuer) :
seulement leur lien et leur sha256, téléchargés quand un add-on en a besoin.

```json
{
  "plugins": [
    { "name": "Vault", "url": "https://github.com/MilkBowl/Vault/releases/download/1.7.3/Vault.jar",
      "sha256": "…", "note": "facultatif : provenance, licence" }
  ]
}
```

`name` est le `name` du `plugin.yml` du plugin (et `provides` ses autres noms). Pour le sha256 :

```bash
curl -fsSL "$URL" | sha256sum
```

Une dépendance obligatoire absente fait échouer la génération (`MISSING_DEPENDENCY`) ; une
dépendance facultative absente donne un avertissement (les syntaxes qui en dépendent manquent).
Modifier `deps.json` relance les add-ons dont la dernière génération a échoué.

## Mise en place

1. **Images** : le workflow *Extractor images* les construit et les publie à chaque changement
   d'`extractor/` (ou à la main). Après la première publication, passer le paquet
   `skript-docs-extractor` en **public** (*Packages* → paramètres du paquet) : la tâche `extract`
   les tire alors sans se connecter.
2. **Workflow d'extraction** : rien à configurer, il ne lit que GitHub.
3. Un dépôt public sans activité pendant 60 jours voit ses workflows planifiés désactivés par
   GitHub : `status.json`, réécrit chaque mois, produit au moins un commit par mois.

### Nouvelle version de Skript ou de Minecraft

Modifier `extractor/images.json`. Le workflow d'images reconstruit, recalcule la référence de
Skript, teste et publie ; les passages suivants produisent une nouvelle génération de chaque add-on
avec les nouvelles images (20 par passage).

### En local

```bash
docker build -t skript-docs-extractor:26.3-skript2.17.1 -f extractor/image/Dockerfile extractor
```

```bash
extractor/test/run.sh skript-docs-extractor:26.3-skript2.17.1
```

```bash
cd scanner && go build -o ../scanner-bin . && cd ..
```

```bash
./scanner-bin plan --addon mongosk > plan.json
```

```bash
SCANNER_IMAGE_REPOSITORY=skript-docs-extractor ./scanner-bin extract --job "$(jq -c '.jobs[0]' plan.json)" --out results/0
```

```bash
./scanner-bin publish --plan plan.json --results results
```

`plan` et `validate --check-repos` lisent `GITHUB_TOKEN` s'il est défini (limite de l'API GitHub).
Variables d'`extract` : `SCANNER_IMAGE_REPOSITORY` (images locales), `SCANNER_ALLOWED_HOSTS` (hôtes
de téléchargement, ceux de GitHub par défaut), `SCANNER_MAX_JAR_MB` (64), `SCANNER_TIMEOUT` (4m),
`SCANNER_MEMORY` (1536m), `SCANNER_CPUS` (2), `SCANNER_DOCKER_RUNTIME` (par exemple `runsc`).

## Codes d'erreur

| Code | Signification |
|---|---|
| `DOWNLOAD_FAILED`, `HOST_NOT_ALLOWED`, `JAR_TOO_LARGE` | Le jar n'a pas pu être téléchargé (ou pas depuis un hôte autorisé). |
| `INVALID_JAR`, `NO_PLUGIN_YML` | Ce n'est pas un plugin Bukkit/Paper. |
| `NOT_A_SKRIPT_ADDON`, `SKRIPT_ITSELF` | Le plugin ne dépend pas de Skript, ou c'est Skript lui-même. |
| `MISSING_DEPENDENCY`, `DEPENDENCY_CHECKSUM` | Une dépendance obligatoire manque à `deps.json`, ou son jar ne correspond plus au sha256. |
| `NO_COMPATIBLE_IMAGE` | L'`api-version` demande une version de Minecraft plus récente que toutes les images. |
| `TARGET_NOT_FOUND`, `TARGET_NOT_ENABLED` | Paper a refusé le plugin, ou il ne s'est pas activé : voir le `.log`. |
| `TIMEOUT`, `CONTAINER_FAILED`, `NO_OUTPUT`, `INVALID_OUTPUT`, `EXPORT_CRASHED` | Le serveur ou l'export a échoué : voir le `.log`. |
| `BASELINE_MISSING`, `BASELINE_MISMATCH` | Image mal construite. |
| `INVALID_EXPORT`, `NO_SYNTAX` | L'export a été refusé à la publication, ou il est vide. |
| `SCANNER_ERROR`, `UNKNOWN` | Erreur interne. |

Une release dont aucune pièce jointe n'est un jar utilisable n'est pas planifiée ; le journal du
workflow (*Plan*) le signale.

## Contenu publié

Les descriptions et exemples publiés ici sont ceux que les auteurs des add-ons ont écrits dans leur
code (annotations `@Name`, `@Description`, `@Examples`…). Un auteur qui ne souhaite pas que la
documentation de son add-on figure ici peut ouvrir une issue : l'add-on sera retiré d'`addons.json`
et son dossier supprimé.
