# Quickstart & Demo — Maintenance Orchestrator
### Mini guida 🇮🇹 / 🇬🇧 — uso del software e simulazione su Docker Desktop + Kubernetes

> 🇮🇹 Questa guida mostra **come usare** il Maintenance Orchestrator e **come simularne
> l'utilizzo** su Docker Desktop, sia con il Kubernetes integrato (single-node) sia con
> un cluster multi-nodo `kind` (che gira sullo stesso motore Docker di Docker Desktop).
>
> 🇬🇧 This guide shows **how to use** the Maintenance Orchestrator and **how to simulate
> it** on Docker Desktop, both with the built-in single-node Kubernetes and with a
> multi-node `kind` cluster (which runs on the same Docker engine as Docker Desktop).

---

## 0. Concetti in 30 secondi / 30-second mental model

🇮🇹 L'orchestratore è un **controller Kubernetes** guidato da due CRD:
- **`MaintenancePolicy`** (`mpol`): i guardrail di cluster (protezione control-plane,
  max nodi non disponibili, concorrenza, soglia di fallimenti, finestre, ecc.).
- **`MaintenanceRequest`** (`mreq`): una singola operazione di manutenzione auditabile
  su nodi/pool. Ogni richiesta attraversa una **state machine**:
  `Pending → Validating → (AwaitingApproval) → Planned → Executing → Completed`
  (oppure `Blocked / Failed / Cancelled / Paused`).

🇬🇧 The orchestrator is a **Kubernetes controller** driven by two CRDs:
- **`MaintenancePolicy`** (`mpol`): cluster guardrails (control-plane protection, max
  unavailable nodes, concurrency, failure threshold, windows, etc.).
- **`MaintenanceRequest`** (`mreq`): a single auditable node/pool maintenance operation.
  Each request flows through a **state machine**:
  `Pending → Validating → (AwaitingApproval) → Planned → Executing → Completed`
  (or `Blocked / Failed / Cancelled / Paused`).

**Modes:** `DryRun` (analizza, non muta / analyze, never mutates) ·
`Advisory` (rivaluta in continuo, non muta / continuously re-evaluates, never mutates) ·
`Execute` (cordon → drain → uncordon).

---

## 1. Prerequisiti / Prerequisites

🇮🇹 Necessari / 🇬🇧 Required:
- **Docker Desktop** ≥ 4.x con **Kubernetes abilitato**
  (Settings → Kubernetes → *Enable Kubernetes*).
- **`kubectl`** nel PATH (Docker Desktop lo include).
- **Go** ≥ 1.22 e **make** (solo se costruisci l'immagine dal sorgente / only to build
  the image from source).
- *(Solo per il percorso B / Path B only)* **`kind`** ≥ 0.20
  (`brew install kind`, `choco install kind`, o vedi https://kind.sigs.k8s.io).

Verifica / Verify:
```bash
kubectl version --client
kubectl config use-context docker-desktop   # IT: seleziona il cluster / EN: select cluster
kubectl get nodes
```

---

## 2. Build dell'immagine / Build the image

🇮🇹 Dalla root del repository costruisci l'immagine del controller. Con il Kubernetes di
Docker Desktop **non serve push**: il cluster condivide il daemon Docker, quindi
l'immagine locale è già visibile (il Deployment usa `imagePullPolicy: IfNotPresent`).

🇬🇧 From the repo root, build the controller image. With Docker Desktop's Kubernetes you
**don't need to push**: the cluster shares the Docker daemon, so the local image is
already visible (the Deployment uses `imagePullPolicy: IfNotPresent`).

```bash
# IT: opzione A — Makefile / EN: option A — Makefile
make docker-build IMG=maintenance-orchestrator:latest

# IT: opzione B — docker diretto / EN: option B — plain docker
docker build -t maintenance-orchestrator:latest .
```

---

## 3. Percorso A — Docker Desktop Kubernetes (single-node, sicuro)
## Path A — Docker Desktop Kubernetes (single-node, safe)

🇮🇹 Il cluster integrato ha **un solo nodo** (`docker-desktop`) che è **control-plane**,
quindi protetto dai guardrail. È perfetto per dimostrare **DryRun / Advisory**,
**preflight**, **planning**, **risk score** e le **safety guardrail** — senza alcuna
disruzione reale. (Un `Execute` su quel nodo viene volutamente **bloccato**: è la
sicurezza che funziona.)

🇬🇧 The built-in cluster has **a single node** (`docker-desktop`) which is
**control-plane**, hence protected by the guardrails. It's ideal to demonstrate
**DryRun / Advisory**, **preflight**, **planning**, **risk score** and the **safety
guardrails** — with zero real disruption. (An `Execute` against that node is
deliberately **blocked**: that's the safety working as intended.)

### 3.1 Installa CRD, RBAC, policy e controller / Install CRDs, RBAC, policy and controller

```bash
# IT: tutto in un colpo (CRD + RBAC + namespace + manager) / EN: all at once
kubectl apply -k deploy

# IT: crea la policy di default attesa dal controller / EN: create the default policy
kubectl apply -f deploy/samples/policy-cluster-default.yaml

# IT: attendi che il controller sia pronto / EN: wait for the controller to be ready
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator
```

> 🇮🇹 Se l'immagine non fosse rilevata, forzala: 🇬🇧 If the image isn't picked up, force it:
> ```bash
> kubectl -n maintenance-orchestrator-system set image \
>   deploy/maintenance-orchestrator manager=maintenance-orchestrator:latest
> ```

### 3.2 Crea dei nodi finti per il DryRun / Create fake nodes for the DryRun

🇮🇹 Il sample `node-dryrun` punta a `worker-1`/`worker-2`, che su Docker Desktop non
esistono. Per una demo realistica del DryRun creiamo due **nodi fittizi** (oggetti API,
nessuna VM): il preflight li vedrà e produrrà un piano.

🇬🇧 The `node-dryrun` sample targets `worker-1`/`worker-2`, which don't exist on Docker
Desktop. For a realistic DryRun demo we create two **fake nodes** (API objects, no VMs):
preflight will see them and produce a plan.

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

### 3.3 Lancia un DryRun e leggi il piano / Run a DryRun and read the plan

```bash
kubectl apply -f deploy/samples/mreq-node-dryrun.yaml

# IT: stato sintetico / EN: short status
kubectl get mreq node-dryrun

# IT: dettaglio: fase, preflight, piano, risk score / EN: detail: phase, preflight, plan, risk
kubectl get mreq node-dryrun -o yaml | less
```

🇮🇹 Cosa guardare in `.status`: `phase: Completed`, l'array `preflight[]` (con
`status: Pass/Warn/Fail`, `code`, `message`), e `plan` (con `batches`, `riskScore`,
`impact`). In DryRun **nulla viene modificato**.

🇬🇧 What to look at under `.status`: `phase: Completed`, the `preflight[]` array (with
`status: Pass/Warn/Fail`, `code`, `message`), and `plan` (with `batches`, `riskScore`,
`impact`). In DryRun **nothing is mutated**.

```bash
# IT: estrai solo le parti interessanti / EN: extract just the interesting parts
kubectl get mreq node-dryrun -o jsonpath='{.status.phase}{"\n"}'
kubectl get mreq node-dryrun -o jsonpath='{range .status.preflight[*]}{.status}{"\t"}{.code}{"\t"}{.message}{"\n"}{end}'
kubectl get mreq node-dryrun -o jsonpath='{.status.plan.riskScore}{"\n"}{.status.plan.riskFactors}{"\n"}'
```

### 3.4 Dimostra il guardrail control-plane / Demonstrate the control-plane guardrail

🇮🇹 Proviamo un `Execute` sul nodo reale `docker-desktop`: deve finire **Blocked** per
protezione control-plane.

🇬🇧 Let's try an `Execute` on the real `docker-desktop` node: it must end up **Blocked**
by control-plane protection.

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
# IT atteso / EN expected: Blocked   blocked by failed preflight checks (CONTROL_PLANE_PROTECTED)
```

### 3.5 Metriche e log / Metrics and logs

```bash
# IT: log strutturati JSON / EN: structured JSON logs
kubectl -n maintenance-orchestrator-system logs deploy/maintenance-orchestrator -f

# IT: metriche Prometheus (porta-forward) / EN: Prometheus metrics (port-forward)
kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-metrics 8080:8080 &
curl -s localhost:8080/metrics | grep -E 'maintenance_(requests|success|failure)|preflight_failures|blocked_drains|active_maintenances'
```

### 3.6 Pulizia parziale / Partial cleanup

```bash
kubectl delete mreq node-dryrun try-controlplane --ignore-not-found
kubectl delete node worker-1 worker-2 --ignore-not-found
```

---

## 4. Percorso B — `kind` multi-nodo: Execute reale / real Execute
## Path B — multi-node `kind`: watch a real cordon/drain/uncordon

🇮🇹 Per vedere **davvero** cordon → drain → uncordon su più nodi serve un cluster
multi-nodo. `kind` lo crea usando il Docker di Docker Desktop: nessuna VM aggiuntiva.

🇬🇧 To **actually** watch cordon → drain → uncordon across nodes you need a multi-node
cluster. `kind` creates one using Docker Desktop's Docker: no extra VMs.

### 4.1 Crea il cluster / Create the cluster

```bash
cat <<EOF | kind create cluster --name mo-demo --config -
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
  - role: worker
  - role: worker
EOF

kubectl config use-context kind-mo-demo
kubectl get nodes -o wide
```

### 4.2 Carica l'immagine nel cluster kind / Load the image into the kind cluster

🇮🇹 `kind` ha il suo registro interno: l'immagine locale va **caricata** (non basta
averla in Docker Desktop).
🇬🇧 `kind` has its own internal registry: the local image must be **loaded** (having it
in Docker Desktop is not enough).

```bash
kind load docker-image maintenance-orchestrator:latest --name mo-demo
```

### 4.3 Installa e prepara un carico con PDB / Install and prepare a workload with a PDB

```bash
kubectl apply -k deploy
kubectl apply -f deploy/samples/policy-cluster-default.yaml
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator

# IT: etichetta i worker come "pool" demo / EN: label the workers as a demo "pool"
for n in $(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o name); do
  kubectl label "$n" pool=demo --overwrite
done

# IT: app con 3 repliche + PDB (minAvailable 2) per vedere il rispetto del PDB
# EN: 3-replica app + PDB (minAvailable 2) to watch PDB being honored
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

> ⚠️ 🇮🇹 **Tip:** non drenare il worker che ospita il controller, o si interrompe la demo.
> Controlla dove gira e scegli un altro nodo come target.
> 🇬🇧 **Tip:** don't drain the worker hosting the controller, or you'll interrupt the demo.
> Check where it runs and target a different node.
> ```bash
> kubectl -n maintenance-orchestrator-system get pod -o wide   # IT/EN: nota il NODE
> kubectl get pods -A -o wide --field-selector spec.nodeName=mo-demo-worker
> ```

### 4.4 DryRun, poi Execute su un nodo / DryRun, then Execute on one node

🇮🇹 Sostituisci `mo-demo-worker2` con un worker **diverso** da quello del controller.
🇬🇧 Replace `mo-demo-worker2` with a worker **different** from the controller's.

```bash
# --- DryRun: analizza senza mutare / analyze without mutating
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

# --- Execute: passa a Execute la STESSA richiesta / flip the SAME request to Execute
kubectl patch mreq drain-one --type=merge -p '{"spec":{"mode":"Execute"}}'

# IT: osserva fase, riepilogo e per-nodo / EN: watch phase, summary and per-node
watch -n2 "kubectl get mreq drain-one -o jsonpath='{.status.phase}{\" \"}{.status.summary}{\"\n\"}'; \
  echo '--- nodes ---'; kubectl get mreq drain-one -o jsonpath='{range .status.nodes[*]}{.node}{\"\t\"}{.phase}{\"\t\"}{.message}{\"\n\"}{end}'"
```

🇮🇹 In parallelo, guarda il nodo passare a `SchedulingDisabled` e i pod ricollocarsi
(il PDB `minAvailable: 2` viene rispettato dall'eviction API):
🇬🇧 In parallel, watch the node go `SchedulingDisabled` and pods reschedule (the
`minAvailable: 2` PDB is honored by the eviction API):

```bash
kubectl get nodes
kubectl get pods -o wide -w
```

🇮🇹 A fine corsa: `phase: Completed`, il nodo viene **uncordonato** (`uncordonAfter: true`)
e torna `Ready,SchedulingEnabled`.
🇬🇧 When done: `phase: Completed`, the node is **uncordoned** (`uncordonAfter: true`) and
returns `Ready,SchedulingEnabled`.

### 4.5 Manutenzione di pool con approvazione manuale / Pool maintenance with manual approval

🇮🇹 Manutenzione “rolling” di tutti i worker `pool=demo`, **un nodo alla volta**, con
**approvazione manuale** prima del drain.
🇬🇧 Rolling maintenance of all `pool=demo` workers, **one node at a time**, with **manual
approval** before draining.

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

# IT: la richiesta si ferma in AwaitingApproval / EN: it stops at AwaitingApproval
kubectl get mreq pool-demo -o jsonpath='{.status.phase}{"\t"}{.status.approvalGate}{"\n"}'

# IT: APPROVA il gate di drain / EN: APPROVE the drain gate
kubectl patch mreq pool-demo --type=merge \
  -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Approved","approvedBy":"sre@example.com"}]}}}'

# IT: (in alternativa) RIFIUTA / EN: (alternatively) REJECT  -> request becomes Cancelled
# kubectl patch mreq pool-demo --type=merge \
#   -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Rejected","approvedBy":"sre@example.com"}]}}}'
```

### 4.6 Controllo runtime: pause / resume / cancel

```bash
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"pause":true}}'    # IT: sospendi / EN: hold
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"pause":false}}'   # IT: riprendi / EN: resume
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"cancel":true}}'   # IT: annulla / EN: cancel
```

### 4.7 Aggiornare la versione Kubernetes / Upgrade the Kubernetes version

🇮🇹 Con `spec.upgrade` la manutenzione **sostituisce** il nodo dopo il drain:
elimina la sua `Machine` (OpenShift `machine.openshift.io` o Cluster API
`cluster.x-k8s.io`) così il pool lo ricrea alla versione del template. Richiede
`allowNodeReplacement: true` nella policy (già nel sample `cluster-default`).

🇬🇧 With `spec.upgrade` the maintenance **replaces** the node after draining: it
deletes its `Machine` (OpenShift or Cluster API) so the pool recreates it at the
template's version. Requires `allowNodeReplacement: true` in the policy (already
set in the `cluster-default` sample).

> ⚠️ 🇮🇹 **`kind` non ha una Machine API**, quindi il preflight risponde
> `MACHINE_NOT_FOUND` e il nodo va in `Blocked`: la sostituzione si prova su
> **OpenShift** o un cluster **Cluster API/CAPI** reale. Vedi
> [`deploy/samples/mreq-pool-upgrade.yaml`](../deploy/samples/mreq-pool-upgrade.yaml).
> 🇬🇧 **`kind` has no Machine API**, so preflight returns `MACHINE_NOT_FOUND` and the
> node goes `Blocked`; try replacement on real **OpenShift** or **Cluster API**.

```bash
# IT: su un cluster con Machine API / EN: on a cluster with a Machine API
kubectl apply -f deploy/samples/mreq-pool-upgrade.yaml
kubectl get mreq pool-upgrade -o jsonpath='{range .status.nodes[*]}{.node}{"  "}{.phase}{"  "}{.message}{"\n"}{end}'
# IT: fasi attese / EN: expected phases: ... Draining -> Replacing -> AwaitingReplacement -> Completed
```

### 4.8 Pulizia / Cleanup

```bash
kind delete cluster --name mo-demo
```

---

## 5. Riferimento comandi / Command cheat-sheet

| Azione / Action | Comando / Command |
|---|---|
| Lista richieste / List requests | `kubectl get mreq` |
| Dettaglio / Detail | `kubectl get mreq <name> -o yaml` |
| Solo fase / Phase only | `kubectl get mreq <name> -o jsonpath='{.status.phase}'` |
| Preflight | `kubectl get mreq <name> -o jsonpath='{range .status.preflight[*]}{.status}{" "}{.code}{"\n"}{end}'` |
| Piano / Plan | `kubectl get mreq <name> -o jsonpath='{.status.plan}'` |
| Per-nodo / Per-node | `kubectl get mreq <name> -o jsonpath='{range .status.nodes[*]}{.node}{" "}{.phase}{"\n"}{end}'` |
| Policy | `kubectl get mpol cluster-default -o yaml` |
| Approva drain / Approve drain | `kubectl patch mreq <name> --type=merge -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Approved","approvedBy":"me"}]}}}'` |
| Pausa / Pause | `kubectl patch mreq <name> --type=merge -p '{"spec":{"pause":true}}'` |
| Annulla / Cancel | `kubectl patch mreq <name> --type=merge -p '{"spec":{"cancel":true}}'` |
| Log | `kubectl -n maintenance-orchestrator-system logs deploy/maintenance-orchestrator -f` |
| Metriche / Metrics | `kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-metrics 8080:8080` |

---

## 6. Troubleshooting

| Sintomo / Symptom | Causa & rimedio / Cause & fix |
|---|---|
| Pod controller `ImagePullBackOff` | 🇮🇹 Immagine non nel cluster. 🇬🇧 Image not in the cluster. Docker Desktop: rebuild + `set image`. kind: `kind load docker-image maintenance-orchestrator:latest --name mo-demo`. |
| `mreq` resta in `Validating`/`Blocked` | 🇮🇹 Leggi `.status.preflight[]` per il `code` fallito. 🇬🇧 Read `.status.preflight[]` for the failing `code` (e.g. `CONTROL_PLANE_PROTECTED`, `TOO_MANY_UNAVAILABLE`, `INSUFFICIENT_CAPACITY`). |
| `Execute` su control-plane non parte / won't start | 🇮🇹 È voluto: protezione attiva. 🇬🇧 Intended: protection is on. Override solo in test: `spec.allowControlPlane: true` **e** policy con `protectControlPlane: false`. |
| Drain fermo su `Blocked` con `BlockReason: PDB` | 🇮🇹 Un PDB nega l'eviction (corretto!). 🇬🇧 A PDB denies eviction (correct!). Aumenta le repliche o allenta il PDB. |
| Resta `AwaitingApproval` | 🇮🇹 Manca la decisione del gate. 🇬🇧 The gate decision is missing — patch the `approval.gates[]`. |
| `kubectl apply -k deploy` errore / error | 🇮🇹 Aggiorna kubectl (kustomize integrato) o usa `kubectl kustomize deploy | kubectl apply -f -`. 🇬🇧 Update kubectl or pipe through `kubectl kustomize`. |
| Metriche vuote / empty metrics | 🇮🇹 Verifica il port-forward sulla porta 8080 e il nome service. 🇬🇧 Check the port-forward on 8080 and the service name. |

---

## 7. Note importanti / Important notes

- 🇮🇹 **Single-node = niente drain reale.** Su Docker Desktop usa `DryRun`/`Advisory`; per
  `Execute` reale usa `kind` multi-nodo (Percorso B).
  🇬🇧 **Single-node = no real drain.** On Docker Desktop use `DryRun`/`Advisory`; for a real
  `Execute` use multi-node `kind` (Path B).
- 🇮🇹 **`Advisory`** è come `DryRun` ma **non termina**: rivaluta in continuo e tiene il
  piano aggiornato in `.status` — utile per un cruscotto di rischio.
  🇬🇧 **`Advisory`** is like `DryRun` but **never finishes**: it keeps re-evaluating and
  refreshing the plan in `.status` — handy as a risk dashboard.
- 🇮🇹 **Force eviction** è disabilitata di default: richiede `spec.force: true` **e**
  `allowForceEviction: true` nella policy. 🇬🇧 **Force eviction** is off by default: needs
  both `spec.force: true` and policy `allowForceEviction: true`.
- 🇮🇹 Per OpenShift sostituisci `kubectl` con `oc`; i nodi in aggiornamento MCO vengono
  marcati `Skipped`. 🇬🇧 On OpenShift use `oc`; nodes mid-MCO-update are marked `Skipped`.
