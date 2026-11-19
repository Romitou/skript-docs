# skript-docs

Syntaxes des add-ons [Skript](https://github.com/SkriptLang/Skript), **extraites automatiquement de
leurs jars** et publiées ici en JSON, à jour. Source de la documentation des add-ons de
[Skript-MC](https://skript-mc.fr), et utilisables par n'importe quel outil.

Les syntaxes ne sont pas lues dans le code source mais **sur un vrai serveur** : chaque version
d'add-on est lancée dans un serveur Paper + Skript jetable, sans réseau, avec un plugin d'export qui
écrit ce que l'add-on a réellement enregistré. Les motifs construits à l'exécution (helpers,
constantes, fichiers de langue, enregistrement conditionnel…) sont donc exacts.

## Les données

```
index.json                    une ligne par add-on : dernière version extraite, dernier essai
addons/<slug>/index.json      les versions traitées, réussies ou non
addons/<slug>/<version>.json  les syntaxes d'une version
addons/<slug>/<version>.log   fin du journal du serveur, pour une extraction en échec
status.json                   mois du dernier passage
deps.json                     catalogue des dépendances (voir plus bas)
excluded.json                 add-ons jamais extraits (à la demande de leur auteur)
```

Le `slug` est celui de l'add-on sur skript-mc.fr (`/docs/<slug>`), et les `id` des add-ons et des
versions sont ceux de son API. Pour suivre les changements, lire `index.json` puis le fichier
`latest.file` des add-ons dont `latest.jarSha256` a changé. Pour du contenu stable, pointer une
URL `raw.githubusercontent.com` sur un commit plutôt que sur `main`.

### `index.json`

```json
{
  "formatVersion": 1,
  "addons": [{
    "id": 12, "slug": "mongosk", "name": "MongoSK",
    "latest": { "label": "3.0.0", "file": "addons/mongosk/3.0.0.json", "syntaxCount": 53,
                "jarSha256": "001dfe68…", "extractedAt": "2026-11-19T16:52:07Z" },
    "last": { "label": "3.0.0", "status": "succeeded" }
  }]
}
```

`latest` est la dernière version extraite avec succès (`null` s'il n'y en a pas), `last` la
dernière version essayée, avec `errorCode` si elle a échoué.

### `addons/<slug>/index.json`

Les versions de la plus récente à la plus ancienne. Pour chacune : `label`, `versionId`,
`versionDate`, `jarUrl`, `jarSha256`, `status` (`succeeded` | `failed`), `errorCode`,
`errorMessage`, `file`, `log`, `syntaxCount`, `plugin` (`name`, `version`, `apiVersion` du
`plugin.yml`), `image`, `minecraft`, `skript`, `dependencies` (plugins installés à côté),
`warnings`, `extractedAt`. `latest` désigne la version la plus récente réussie.

### `addons/<slug>/<version>.json`

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
toutes les 6 h ─► plan : add-ons et versions publiés par l'API Skript-MC
                         └─ pour chaque add-on, la dernière release stable avec un .jar
                            qui n'a pas encore été traitée
               ─► extract (une tâche par version, aucune permission d'écriture, aucun secret) :
                         télécharge le jar, lit plugin.yml, installe les dépendances de deps.json,
                         lance le conteneur d'extraction → résultat en artefact
               ─► publish (contents: write, n'exécute aucun code d'add-on) :
                         ne retient que les résultats des versions planifiées, les vérifie et les
                         nettoie, écrit addons/ et index.json, committe
```

| Dossier | Rôle |
|---|---|
| `scanner/` | Programme Go : `plan`, `extract`, `publish`. |
| `extractor/exporter/` | Plugin Bukkit chargé dans le serveur : exporte les syntaxes de l'add-on cible puis arrête le serveur. |
| `extractor/image/` | Image Docker : Paper + Skript patchés à la construction, référence de Skript enregistrée, démarrage hors ligne. |
| `extractor/images.json` | Les images (Minecraft, Java, Skript), lues par les workflows et le scanner. |
| `extractor/test-addon/`, `extractor/test/run.sh` | Add-on minimal et test de bout en bout d'une image. |
| `.github/workflows/extract.yml` | Le cycle ci-dessus. |
| `.github/workflows/images.yml` | Construit, teste et publie les images sur GHCR. |

Une version déjà réussie n'est plus extraite (sauf `force`, ou si son lien de jar change). Une
version en échec est réessayée quand `extractor/images.json` ou `deps.json` changent : ajouter une
dépendance manquante suffit à relancer les add-ons qui l'attendaient. Un échec ne remplace jamais
un succès sur le même jar. Chaque passage traite au plus 20 versions ; la suite attend le suivant.

Lancement manuel : onglet *Actions* → *Extract syntaxes* → *Run workflow*, avec un `slug` pour un
seul add-on, et `force` pour refaire une extraction réussie.

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
  résultats comme non fiables : l'identité de ce qui est publié (add-on, version, jar) vient du plan,
  calculé avant toute extraction ; un résultat qui ne correspond à aucune version planifiée est
  ignoré ; les exports sont vérifiés (format, 3 000 syntaxes au plus, champs nettoyés et bornés,
  champs inconnus retirés).
- **Téléchargements** : https uniquement, depuis les hôtes de GitHub (redirections comprises), 64 Mo
  au plus. Les dépendances viennent de `deps.json`, avec leur sha256 vérifié.
- **Côté Skript-MC**, les syntaxes passent encore par un import en dry-run relu par un admin.

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

Une dépendance obligatoire absente fait échouer l'extraction (`MISSING_DEPENDENCY`) ; une
dépendance facultative absente donne un avertissement (les syntaxes qui en dépendent manquent).
Modifier `deps.json` relance automatiquement les extractions en échec.

## Mise en place

1. **Images** : le workflow *Extractor images* les construit et les publie à chaque changement
   d'`extractor/` (ou à la main). Après la première publication, passer le paquet
   `skript-docs-extractor` en **public** (*Packages* → paramètres du paquet) : la tâche `extract`
   les tire alors sans se connecter.
2. **Workflow d'extraction** : rien à configurer. Il interroge `https://api.skript-mc.fr`
   (`SKRIPT_MC_API` dans `extract.yml`).
3. Un dépôt public sans activité pendant 60 jours voit ses workflows planifiés désactivés par
   GitHub : `status.json`, réécrit chaque mois, produit au moins un commit par mois.

### Nouvelle version de Skript ou de Minecraft

Modifier `extractor/images.json`. Le workflow d'images reconstruit, recalcule la référence de
Skript, teste et publie ; les extractions en échec sont ensuite réessayées avec les nouvelles
images. Les extractions réussies ne sont pas refaites (lancer avec `force` pour cela).

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
./scanner-bin plan --api https://api.skript-mc.fr --addon mongosk > plan.json
```

```bash
SCANNER_IMAGE_REPOSITORY=skript-docs-extractor ./scanner-bin extract --job "$(jq -c '.jobs[0]' plan.json)" --out results/0
```

```bash
./scanner-bin publish --plan plan.json --results results
```

Variables de `extract` : `SCANNER_IMAGE_REPOSITORY` (images locales), `SCANNER_ALLOWED_HOSTS`
(hôtes de téléchargement, GitHub par défaut), `SCANNER_MAX_JAR_MB` (64), `SCANNER_TIMEOUT` (4m),
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

## Contenu publié

Les descriptions et exemples publiés ici sont ceux que les auteurs des add-ons ont écrits dans leur
code (annotations `@Name`, `@Description`, `@Examples`…). Un auteur qui ne souhaite pas que la
documentation de son add-on figure ici peut ouvrir une issue : elle sera retirée (`addons/<slug>/`)
et l'add-on ajouté à `excluded.json`, qu'aucune extraction ne touche plus.
