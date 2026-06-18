# Maintenance Orchestrator for Node/Pool Lifecycle

Controller cloud-native (Go + controller-runtime) per orchestrare in sicurezza la
manutenzione di nodi e pool su **Kubernetes** e **OpenShift**: cordon, drain,
post-check, uncordon opzionale, controllo della concorrenza, workflow di
approvazione e audit completo.

Non è un wrapper di `kubectl drain`: l'intero stato è un oggetto dichiarativo
(CRD) persistito in `etcd`, riconciliato in modo idempotente e resistente ai
restart. L'"API" è la CRD stessa; le operazioni (approve / pause / resume /
cancel) si esprimono come campi di `spec` applicati via `kubectl patch`.

> **Stato:** `v1alpha1` — alpha. Questo repository è in costruzione incrementale.
> Lo **STEP 2** consegna lo scaffold (tipi API, config, logging, metriche,
> bootstrap del manager, build). La logica di riconciliazione (`internal/controller`
> e i package di dominio) arriva nello **STEP 3**: fino ad allora `go build ./...`
> fallisce **solo** per il package `internal/controller` ancora assente.

## Architettura in breve

- **2 CRD cluster-scoped** nel gruppo `maintenance.platform.dev/v1alpha1`:
  - `MaintenanceRequest` (`mreq`) — una singola operazione di manutenzione.
  - `MaintenancePolicy` (`mpol`) — guardrail di cluster (protezione control-plane,
    concorrenza massima, label/taint riservati, finestre consentite, soglie di
    fallimento, headroom di capacità).
- **Modello a poll-and-requeue**: nessuna goroutine in background, nessuna coda
  esterna, nessun DB. Lo stato vive interamente in `.status`.
- **Eviction via `policy/v1`**: rispetta i PodDisruptionBudget; l'eviction forzata
  (delete) è disabilitata di default e gated da `MaintenancePolicy.AllowForceEviction`.
- **Tre modalità**: `DryRun`, `Advisory`, `Execute`. Nessuna mutazione del cluster
  al di fuori di `Execute`.
- **Convivenza** con Machine Config Operator e cluster-autoscaler (non li orchestra):
  i nodi già `unschedulable` o in aggiornamento MCP vengono marcati `Skipped`.
- **Concorrenza globale** garantita da una singola istanza leader-elected.
- Runtime **non-root**, compatibile con la SCC OpenShift `restricted-v2`.

## Struttura del repository

```
maintenance-orchestrator/
├── go.mod
├── Makefile
├── Dockerfile
├── README.md
├── api/
│   └── v1alpha1/
│       ├── groupversion_info.go
│       ├── shared_types.go
│       ├── maintenancerequest_types.go
│       ├── maintenancepolicy_types.go
│       └── zz_generated.deepcopy.go
├── cmd/
│   └── manager/
│       └── main.go
├── internal/
│   ├── config/        # caricamento config (default → file → env) + validazione
│   ├── logging/        # logger zap → logr
│   ├── metrics/        # collector Prometheus
│   └── controller/     # reconciler + dominio  (STEP 3)
├── deploy/             # CRD, RBAC, manager, samples  (STEP 4)
└── hack/
    ├── boilerplate.go.txt
    └── config.local.yaml
```

## Prerequisiti

- Go **1.22+**
- Accesso a un cluster Kubernetes ≥ 1.22 / OpenShift ≥ 4.9 (eviction `policy/v1`)
- `kubectl`, `make`, Docker/Podman per la build dell'immagine

## Build & sviluppo

```bash
# Popola go.sum e le dipendenze indirette (richiede rete verso proxy.golang.org)
make tidy

# Compila il binario (disponibile dopo lo STEP 3, quando esiste internal/controller)
make build

# Esecuzione locale contro il kubeconfig corrente (config di sviluppo)
make run

# Rigenera DeepCopy / CRD / RBAC dai marker kubebuilder
make generate
make manifests

# Immagine container
make docker-build IMG=registry.example.com/maintenance-orchestrator:dev
```

## Configurazione

Il manager carica la configurazione con precedenza **default → file YAML
(`--config` / `CONFIG_FILE`) → variabili d'ambiente**.

| Variabile                 | Default                                                | Descrizione                                  |
|---------------------------|--------------------------------------------------------|----------------------------------------------|
| `METRICS_ADDR`            | `:8080`                                                | Bind address dell'endpoint `/metrics`        |
| `PROBE_ADDR`              | `:8081`                                                | Bind address di `/healthz` e `/readyz`       |
| `LEADER_ELECTION`         | `true`                                                 | Abilita la leader election                   |
| `LEADER_ELECTION_ID`      | `maintenance-orchestrator.maintenance.platform.dev`    | Nome del Lease                               |
| `RECONCILE_CONCURRENCY`   | `2`                                                    | Reconcile concorrenti per controller         |
| `EVICTION_POLL_INTERVAL`  | `5s`                                                   | Frequenza di ricontrollo di un nodo in drain |
| `GLOBAL_REQUEUE_INTERVAL` | `30s`                                                  | Requeue a regime per le richieste attive     |
| `DEFAULT_DRAIN_TIMEOUT`   | `15m`                                                  | Timeout drain per nodo (se non in spec)       |
| `DEFAULT_GLOBAL_TIMEOUT`  | `2h`                                                   | Timeout globale richiesta (se non in spec)    |
| `LOG_LEVEL`               | `info`                                                 | `debug` \| `info` \| `warn` \| `error`        |
| `LOG_FORMAT`              | `json`                                                 | `json` \| `console`                          |
| `ENABLE_K8S_EVENTS`       | `true`                                                 | Emissione di Event Kubernetes                |
| `DEFAULT_POLICY_NAME`     | `cluster-default`                                      | Policy usata se la richiesta omette `policyRef` |
| `AUDIT_EXPORT_PATH`       | _(vuoto)_                                              | File su cui l'audit logger appende            |
| `DEFAULT_POOL_KEYS`       | label di pool note (OpenShift, EKS, GKE, AKS, Karpenter) | Chiavi label trattate come pool (CSV)        |

## Prossimi step

- **STEP 3** — logica core: `internal/controller` (i due reconciler), state machine,
  preflight, planner, executor, policy, window, approval, audit, kube.
- **STEP 4** — manifest di deploy: namespace, ServiceAccount, ClusterRole/Binding,
  Deployment, Service, ConfigMap, le due CRD, NetworkPolicy, ServiceMonitor, samples.
- **STEP 5** — test unitari (preflight, planner, state machine, approval).
- **STEP 6** — README finale dettagliato.
