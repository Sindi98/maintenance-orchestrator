# Demo & uso — Maintenance Orchestrator

> 🇮🇹 Italiano · 🇬🇧 English version: [`DEMO.en.md`](DEMO.en.md)
>
> 📍 **Per INSTALLARE segui solo [`INSTALL.md`](INSTALL.md).** Questa NON è una guida
> d'installazione: mostra **come usare** l'orchestratore e **come simularlo** su Docker
> Desktop (single-node) o su un cluster multi-nodo `kind`, dando per fatta l'installazione.
> I comandi `kubectl apply -k deploy` qui sotto sono ripetuti solo per autonomia della demo.

---

## 0. Concetti in 30 secondi

L'orchestratore è un **controller Kubernetes** guidato da due CRD:
- **`MaintenancePolicy`** (`mpol`): i guardrail di cluster (protezione control-plane, max
  nodi non disponibili, concorrenza, soglia di fallimenti, finestre, ecc.).
- **`MaintenanceRequest`** (`mreq`): una singola operazione di manutenzione auditabile su
  nodi/pool. Ogni richiesta attraversa una **state machine**:
  `Pending → Validating → (AwaitingApproval) → Planned → Executing → Completed`
  (oppure `Blocked / Failed / Cancelled / Paused`).

**Modi:** `DryRun` (analizza, non muta) · `Advisory` (rivaluta in continuo, non muta) ·
`Execute` (cordon → drain → uncordon).

---

## 1. Prerequisiti

- **Docker Desktop** ≥ 4.x con **Kubernetes abilitato** (Settings → Kubernetes → *Enable
  Kubernetes*).
- **`kubectl`** nel PATH (Docker Desktop lo include).
- **Go** ≥ 1.22 e **make** (solo se costruisci l'immagine dal sorgente).
- *(Solo per il percorso B)* **`kind`** ≥ 0.20 (`brew install kind`, `choco install kind`,
  o vedi https://kind.sigs.k8s.io).

Verifica:
```bash
kubectl version --client
kubectl config use-context docker-desktop
kubectl get nodes
```

---

## 2. Build dell'immagine

Dalla root del repository costruisci l'immagine del controller. Con il Kubernetes di
Docker Desktop **non serve push**: il cluster condivide il daemon Docker, quindi l'immagine
locale è già visibile (il Deployment usa `imagePullPolicy: IfNotPresent`).

```bash
# Opzione A — Makefile
make docker-build IMG=maintenance-orchestrator:latest

# Opzione B — docker diretto
docker build -t maintenance-orchestrator:latest .
```

---

## 3. Percorso A — Docker Desktop Kubernetes (single-node, sicuro)

Il cluster integrato ha **un solo nodo** (`docker-desktop`) che è **control-plane**, quindi
protetto dai guardrail. È perfetto per dimostrare **DryRun / Advisory**, **preflight**,
**planning**, **risk score** e le **safety guardrail** — senza alcuna disruzione reale. (Un
`Execute` su quel nodo viene volutamente **bloccato**: è la sicurezza che funziona.)

### 3.1 Installa CRD, RBAC, policy e controller

```bash
# Tutto in un colpo (CRD + RBAC + namespace + manager)
kubectl apply -k deploy

# Crea la policy di default attesa dal controller
kubectl apply -f deploy/samples/policy-cluster-default.yaml

# Attendi che il controller sia pronto
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator
```

> Se l'immagine non fosse rilevata, forzala:
> ```bash
> kubectl -n maintenance-orchestrator-system set image \
>   deploy/maintenance-orchestrator manager=maintenance-orchestrator:latest
> ```

### 3.2 Crea dei nodi finti per il DryRun

Il sample `node-dryrun` punta a `worker-1`/`worker-2`, che su Docker Desktop non esistono.
Per una demo realistica del DryRun creiamo due **nodi fittizi** (oggetti API, nessuna VM):
il preflight li vedrà e produrrà un piano.

```bash
for n in worker-1 worker-2; do
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Node
metadata:
  name: $n
  labels:
    kubernetes.io/os: linux
    topology.kubernetes.io/zone: zone-a
status:
  conditions:
    - type: Ready
      status: "True"
EOF
done
```

### 3.3 Lancia un DryRun e leggi il piano

```bash
kubectl apply -f deploy/samples/mreq-node-dryrun.yaml

# Stato sintetico
kubectl get mreq node-dryrun

# Dettaglio: fase, preflight, piano, risk score
kubectl get mreq node-dryrun -o yaml | less
```

Cosa guardare in `.status`: `phase: Completed`, l'array `preflight[]` (con
`status: Pass/Warn/Fail`, `code`, `message`), e `plan` (con `batches`, `riskScore`,
`impact`). In DryRun **nulla viene modificato**.

```bash
# Estrai solo le parti interessanti
kubectl get mreq node-dryrun -o jsonpath='{.status.phase}{"\n"}'
kubectl get mreq node-dryrun -o jsonpath='{range .status.preflight[*]}{.status}{"\t"}{.code}{"\t"}{.message}{"\n"}{end}'
kubectl get mreq node-dryrun -o jsonpath='{.status.plan.riskScore}{"\n"}{.status.plan.riskFactors}{"\n"}'
```

### 3.4 Dimostra il guardrail control-plane

Proviamo un `Execute` sul nodo reale `docker-desktop`: deve finire **Blocked** per
protezione control-plane.

```bash
cat <<EOF | kubectl apply -f -
apiVersion: maintenance.platform.dev/v1alpha1
kind: MaintenanceRequest
metadata:
  name: try-controlplane
spec:
  mode: Execute
  reason: "demo: control-plane protection"
  requestedBy: "you@example.com"
  target: { type: Node, nodeNames: ["docker-desktop"] }
  approval: { policy: AutoApprove }
EOF

kubectl get mreq try-controlplane -o jsonpath='{.status.phase}{"\t"}{.status.message}{"\n"}'
# Atteso: Blocked   blocked by failed preflight checks (CONTROL_PLANE_PROTECTED)
```

### 3.5 Metriche e log

```bash
# Log strutturati JSON
kubectl -n maintenance-orchestrator-system logs deploy/maintenance-orchestrator -f

# Metriche Prometheus (port-forward)
kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-metrics 8080:8080 &
curl -s localhost:8080/metrics | grep -E 'maintenance_(requests|success|failure)|preflight_failures|blocked_drains|active_maintenances'
```

### 3.6 Dashboard web

La dashboard integrata è abilitata di default nel ConfigMap (`uiEnabled: true`). Esponila e
aprila nel browser — elenca le richieste con stato **live**, mostra preflight/plan/per-nodo,
crea richieste e fa approve/pausa/cancel.

```bash
kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-ui 8082:8082
# apri  ->  http://localhost:8082
```
> ⚠️ Nessuna autenticazione: solo `ClusterIP` + port-forward (o ingress con auth).

### 3.7 Pulizia parziale

```bash
kubectl delete mreq node-dryrun try-controlplane --ignore-not-found
kubectl delete node worker-1 worker-2 --ignore-not-found
```

---

## 4. Percorso B — `kind` multi-nodo: Execute reale

Per vedere **davvero** cordon → drain → uncordon su più nodi serve un cluster multi-nodo.
`kind` lo crea usando il Docker di Docker Desktop: nessuna VM aggiuntiva.

### 4.1 Crea il cluster

```bash
cat <<EOF | kind create cluster --name mo-demo --config -
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    image: kindest/node:v1.29.4
  - role: worker
    image: kindest/node:v1.29.4
  - role: worker
    image: kindest/node:v1.29.4
  - role: worker
    image: kindest/node:v1.29.4
EOF

kubectl config use-context kind-mo-demo
kubectl get nodes -o wide
```

### 4.2 Carica l'immagine nel cluster kind

`kind` ha il suo registro interno: l'immagine locale va **caricata** (non basta averla in
Docker Desktop).

```bash
kind load docker-image maintenance-orchestrator:latest --name mo-demo
```

### 4.3 Installa e prepara un carico con PDB

```bash
kubectl apply -k deploy
kubectl apply -f deploy/samples/policy-cluster-default.yaml
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator

# Etichetta i worker come "pool" demo
for n in $(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o name); do
  kubectl label "$n" pool=demo --overwrite
done

# App con 3 repliche + PDB (minAvailable 2) per vedere il rispetto del PDB
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata: { name: web, namespace: default }
spec:
  replicas: 3
  selector: { matchLabels: { app: web } }
  template:
    metadata: { labels: { app: web } }
    spec:
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: ScheduleAnyway
          labelSelector: { matchLabels: { app: web } }
      containers:
        - name: web
          image: registry.k8s.io/pause:3.9
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata: { name: web, namespace: default }
spec:
  minAvailable: 2
  selector: { matchLabels: { app: web } }
EOF
kubectl rollout status deploy/web
```

> ⚠️ **Tip:** non drenare il worker che ospita il controller, o si interrompe la demo.
> Controlla dove gira e scegli un altro nodo come target.
> ```bash
> kubectl -n maintenance-orchestrator-system get pod -o wide   # nota il NODE
> kubectl get pods -A -o wide --field-selector spec.nodeName=mo-demo-worker
> ```

### 4.4 DryRun, poi Execute su un nodo

Sostituisci `mo-demo-worker2` con un worker **diverso** da quello del controller.

```bash
# --- DryRun: analizza senza mutare
cat <<EOF | kubectl apply -f -
apiVersion: maintenance.platform.dev/v1alpha1
kind: MaintenanceRequest
metadata: { name: drain-one }
spec:
  mode: DryRun
  reason: "demo drain"
  requestedBy: "you@example.com"
  target: { type: Node, nodeNames: ["mo-demo-worker2"] }
  strategy: Serial
  maxConcurrent: 1
  uncordonAfter: true
  approval: { policy: AutoApprove }
EOF
kubectl get mreq drain-one -o jsonpath='{.status.phase}{"\n"}{.status.plan.impact}{"\n"}'

# --- Execute: passa a Execute la STESSA richiesta
kubectl patch mreq drain-one --type=merge -p '{"spec":{"mode":"Execute"}}'

# Osserva fase, riepilogo e per-nodo
watch -n2 "kubectl get mreq drain-one -o jsonpath='{.status.phase}{\" \"}{.status.summary}{\"\n\"}'; \
  echo '--- nodes ---'; kubectl get mreq drain-one -o jsonpath='{range .status.nodes[*]}{.node}{\"\t\"}{.phase}{\"\t\"}{.message}{\"\n\"}{end}'"
```

In parallelo, guarda il nodo passare a `SchedulingDisabled` e i pod ricollocarsi (il PDB
`minAvailable: 2` viene rispettato dall'eviction API):

```bash
kubectl get nodes
kubectl get pods -o wide -w
```

A fine corsa: `phase: Completed`, il nodo viene **uncordonato** (`uncordonAfter: true`) e
torna `Ready,SchedulingEnabled`.

### 4.5 Manutenzione di pool con approvazione manuale

Manutenzione “rolling” di tutti i worker `pool=demo`, **un nodo alla volta**, con
**approvazione manuale** prima del drain.

```bash
cat <<EOF | kubectl apply -f -
apiVersion: maintenance.platform.dev/v1alpha1
kind: MaintenanceRequest
metadata: { name: pool-demo }
spec:
  mode: Execute
  reason: "rolling demo"
  requestedBy: "sre@example.com"
  target:
    type: NodeSelector
    selector: { matchLabels: { pool: demo } }
  strategy: Serial
  maxConcurrent: 1
  uncordonAfter: true
  approval: { policy: ManualBeforeDrain }
EOF

# La richiesta si ferma in AwaitingApproval
kubectl get mreq pool-demo -o jsonpath='{.status.phase}{"\t"}{.status.approvalGate}{"\n"}'

# APPROVA il gate di drain
kubectl patch mreq pool-demo --type=merge \
  -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Approved","approvedBy":"sre@example.com"}]}}}'

# (in alternativa) RIFIUTA  -> la richiesta diventa Cancelled
# kubectl patch mreq pool-demo --type=merge \
#   -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Rejected","approvedBy":"sre@example.com"}]}}}'
```

### 4.6 Controllo runtime: pause / resume / cancel

```bash
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"pause":true}}'    # sospendi
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"pause":false}}'   # riprendi
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"cancel":true}}'   # annulla
```

### 4.7 Aggiornare la versione Kubernetes (node replacement)

Con `spec.upgrade` la manutenzione **sostituisce** il nodo dopo il drain: elimina la sua
`Machine` (OpenShift `machine.openshift.io` o Cluster API `cluster.x-k8s.io`) così il pool
lo ricrea alla versione del template. Richiede `allowNodeReplacement: true` nella policy
(già nel sample `cluster-default`).

> ⚠️ **`kind` non ha una Machine API**, quindi il preflight risponde `MACHINE_NOT_FOUND` e
> il nodo va in `Blocked`: la sostituzione si prova su **OpenShift** o un cluster
> **Cluster API/CAPI** reale. Vedi
> [`../deploy/samples/mreq-pool-upgrade.yaml`](../deploy/samples/mreq-pool-upgrade.yaml).

```bash
# Su un cluster con Machine API
kubectl apply -f deploy/samples/mreq-pool-upgrade.yaml
kubectl get mreq pool-upgrade -o jsonpath='{range .status.nodes[*]}{.node}{"  "}{.phase}{"  "}{.message}{"\n"}{end}'
# Fasi attese: ... Draining -> Replacing -> AwaitingReplacement -> Completed
```

### 4.8 Pulizia

```bash
kind delete cluster --name mo-demo
```

---

## 5. Riferimento comandi

| Azione | Comando |
|---|---|
| Lista richieste | `kubectl get mreq` |
| Dettaglio | `kubectl get mreq <name> -o yaml` |
| Solo fase | `kubectl get mreq <name> -o jsonpath='{.status.phase}'` |
| Preflight | `kubectl get mreq <name> -o jsonpath='{range .status.preflight[*]}{.status}{" "}{.code}{"\n"}{end}'` |
| Piano | `kubectl get mreq <name> -o jsonpath='{.status.plan}'` |
| Per-nodo | `kubectl get mreq <name> -o jsonpath='{range .status.nodes[*]}{.node}{" "}{.phase}{"\n"}{end}'` |
| Policy | `kubectl get mpol cluster-default -o yaml` |
| Approva drain | `kubectl patch mreq <name> --type=merge -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Approved","approvedBy":"me"}]}}}'` |
| Pausa | `kubectl patch mreq <name> --type=merge -p '{"spec":{"pause":true}}'` |
| Annulla | `kubectl patch mreq <name> --type=merge -p '{"spec":{"cancel":true}}'` |
| Log | `kubectl -n maintenance-orchestrator-system logs deploy/maintenance-orchestrator -f` |
| Metriche | `kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-metrics 8080:8080` |
| Dashboard | `kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-ui 8082:8082` |

---

## 6. Troubleshooting

| Sintomo | Causa & rimedio |
|---|---|
| Pod controller `ImagePullBackOff` | Immagine non nel cluster. Docker Desktop: rebuild + `set image`. kind: `kind load docker-image maintenance-orchestrator:latest --name mo-demo`. |
| `mreq` resta in `Validating`/`Blocked` | Leggi `.status.preflight[]` per il `code` fallito (es. `CONTROL_PLANE_PROTECTED`, `TOO_MANY_UNAVAILABLE`, `INSUFFICIENT_CAPACITY`). |
| `Execute` su control-plane non parte | È voluto: protezione attiva. Override solo in test: `spec.allowControlPlane: true` **e** policy con `protectControlPlane: false`. |
| Drain fermo su `Blocked` con `BlockReason: PDB` | Un PDB nega l'eviction (corretto!). Aumenta le repliche o allenta il PDB. |
| Resta `AwaitingApproval` | Manca la decisione del gate — patcha `approval.gates[]`. |
| `kubectl apply -k deploy` errore | Aggiorna kubectl (kustomize integrato) o usa `kubectl kustomize deploy \| kubectl apply -f -`. |
| Metriche vuote | Verifica il port-forward sulla porta 8080 e il nome service. |

---

## 7. Note importanti

- **Single-node = niente drain reale.** Su Docker Desktop usa `DryRun`/`Advisory`; per
  `Execute` reale usa `kind` multi-nodo (Percorso B).
- **`Advisory`** è come `DryRun` ma **non termina**: rivaluta in continuo e tiene il piano
  aggiornato in `.status` — utile per un cruscotto di rischio.
- **Force eviction** è disabilitata di default: richiede `spec.force: true` **e**
  `allowForceEviction: true` nella policy.
- Per OpenShift sostituisci `kubectl` con `oc`; i nodi in aggiornamento MCO vengono marcati
  `Skipped`.
