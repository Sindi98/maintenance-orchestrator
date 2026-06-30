# Demo & uso — Maintenance Orchestrator

> 🇮🇹 Italiano · 🇬🇧 English version: [`DEMO.en.md`](DEMO.en.md)
>
> 📍 **Per INSTALLARE segui solo [`INSTALL.md`](INSTALL.md).** Questa guida mostra **come
> usare** l'orchestratore su un cluster **multi-nodo** `kind` — l'unico ambiente in cui si
> vede davvero `cordon → drain → uncordon`. Niente esempi single-node: su un solo nodo
> (control-plane) i guardrail bloccano tutto di proposito e non succede nulla di reale.

---

## 0. Concetti in 30 secondi

L'orchestratore è un **controller Kubernetes** guidato da due CRD:
- **`MaintenancePolicy`** (`mpol`): i guardrail di cluster (protezione control-plane, max
  nodi non disponibili, concorrenza, soglia di fallimenti, finestre, ecc.).
- **`MaintenanceRequest`** (`mreq`): una singola operazione di manutenzione auditabile. Ogni
  richiesta attraversa una **state machine**:
  `Pending → Validating → (AwaitingApproval) → Planned → Executing → Completed`
  (oppure `Blocked / Failed / Cancelled / Paused`).

**Modi:** `DryRun` (analizza, non muta) · `Advisory` (rivaluta in continuo, non muta) ·
`Execute` (cordon → drain → uncordon).

---

## 1. Prerequisiti

- **`kind`** ≥ 0.20 (`brew install kind`, `choco install kind`, o https://kind.sigs.k8s.io).
- **Docker** in esecuzione (kind crea i nodi come container).
- **`kubectl`** nel PATH.
- **Go** ≥ 1.22 e **make** solo se costruisci l'immagine dal sorgente.

```bash
kind version && docker version --format '{{.Server.Version}}' && kubectl version --client
```

---

## 2. Crea un cluster kind multi-nodo

1 control-plane + 3 worker (il minimo per vedere drain/uncordon reali):
```bash
cat <<EOF | kind create cluster --name mo-demo --config -
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    image: kindest/node:v1.27.3      # <-- versione VECCHIA
  - role: worker
    image: kindest/node:v1.27.3
  - role: worker
    image: kindest/node:v1.27.3
  - role: worker
    image: kindest/node:v1.27.3
EOF

kubectl config use-context kind-mo-demo
kubectl get nodes -o wide            # colonna VERSION = v1.27.3
```

---

## 3. Build e carica l'immagine

`kind` ha un registro interno: l'immagine locale va **caricata** (non basta averla in Docker).
```bash
docker build -t maintenance-orchestrator:latest .
kind load docker-image maintenance-orchestrator:latest --name mo-demo
```

---

## 4. Installa e prepara un carico con PDB

```bash
kubectl apply -k deploy
kubectl apply -f deploy/samples/policy-cluster-default.yaml
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator

# Etichetta i worker come "pool" demo
for n in $(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o name); do
  kubectl label "$n" pool=demo --overwrite
done

# App a 3 repliche + PDB (minAvailable 2) per vedere il rispetto del PDB durante il drain
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
> ```

---

## 5. DryRun: analizza un nodo (nessuna modifica)

Sostituisci `mo-demo-worker2` con un worker **diverso** da quello del controller.
```bash
cat <<EOF | kubectl apply -f -
apiVersion: maintenance.platform.dev/v1alpha1
kind: MaintenanceRequest
metadata: { name: drain-one }
spec:
  mode: DryRun
  reason: "analisi pre-manutenzione"
  requestedBy: "you@example.com"
  target: { type: Node, nodeNames: ["mo-demo-worker2"] }
  strategy: Serial
  maxConcurrent: 1
  uncordonAfter: true
  approval: { policy: AutoApprove }
EOF

# .status: phase Completed, preflight[], plan (risk score, impatto). Nessuna mutazione.
kubectl get mreq drain-one -o jsonpath='{.status.phase}{"\n"}'
kubectl get mreq drain-one -o jsonpath='{range .status.preflight[*]}{.status}{"\t"}{.code}{"\t"}{.message}{"\n"}{end}'
kubectl get mreq drain-one -o jsonpath='riskScore={.status.plan.riskScore}{"\n"}impact={.status.plan.impact}{"\n"}'
```

---

## 6. Execute: drain reale di un nodo

Passa a `Execute` la **stessa** richiesta e osserva l'avanzamento:
```bash
kubectl patch mreq drain-one --type=merge -p '{"spec":{"mode":"Execute"}}'

watch -n2 "kubectl get mreq drain-one -o jsonpath='{.status.phase}{\" \"}{.status.summary}{\"\n\"}'; \
  echo '--- nodes ---'; kubectl get mreq drain-one -o jsonpath='{range .status.nodes[*]}{.node}{\"\t\"}{.phase}{\"\t\"}{.message}{\"\n\"}{end}'"
```
In parallelo, vedi il nodo passare a `SchedulingDisabled` e i pod ricollocarsi (il PDB
`minAvailable: 2` viene rispettato dall'eviction API):
```bash
kubectl get nodes
kubectl get pods -o wide -w
```
A fine corsa: `phase: Completed`, il nodo viene **uncordonato** e torna `Ready,SchedulingEnabled`.

---

## 7. Guardrail control-plane (deve bloccare)

Un `Execute` sul control-plane deve finire **Blocked** per protezione — è la sicurezza:
```bash
CP=$(kubectl get nodes -l node-role.kubernetes.io/control-plane -o jsonpath='{.items[0].metadata.name}')
cat <<EOF | kubectl apply -f -
apiVersion: maintenance.platform.dev/v1alpha1
kind: MaintenanceRequest
metadata: { name: try-controlplane }
spec:
  mode: Execute
  reason: "demo: protezione control-plane"
  requestedBy: "you@example.com"
  target: { type: Node, nodeNames: ["$CP"] }
  approval: { policy: AutoApprove }
EOF
kubectl get mreq try-controlplane -o jsonpath='{.status.phase}{"\t"}{.status.message}{"\n"}'
# Atteso: Blocked  (preflight CONTROL_PLANE_PROTECTED)
```

---

## 8. Manutenzione di pool con approvazione manuale

Rolling di tutti i worker `pool=demo`, **un nodo alla volta**, con **approvazione manuale**:
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

# Si ferma in AwaitingApproval
kubectl get mreq pool-demo -o jsonpath='{.status.phase}{"\t"}{.status.approvalGate}{"\n"}'

# APPROVA il gate di drain
kubectl patch mreq pool-demo --type=merge \
  -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Approved","approvedBy":"sre@example.com"}]}}}'

# (in alternativa) RIFIUTA  -> la richiesta diventa Cancelled
# kubectl patch mreq pool-demo --type=merge \
#   -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Rejected","approvedBy":"sre@example.com"}]}}}'
```

---

## 9. Controllo runtime: pause / resume / cancel

```bash
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"pause":true}}'    # sospendi
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"pause":false}}'   # riprendi
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"cancel":true}}'   # annulla
```

---

## 10. Dashboard web

```bash
kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-ui 8082:8082
# apri  ->  http://localhost:8082   (lista live, dettaglio, crea richieste, approve/pausa/cancel)
```
> ⚠️ Nessuna autenticazione: solo `ClusterIP` + port-forward (o ingress con auth).

---

## 11. Aggiornare la versione Kubernetes dei nodi

Su `kind` **non** si usa `spec.upgrade` (kind non ha Machine API e non supporta l'upgrade
in-place): si **ricrea** il cluster con una node-image più recente — aggiorna worker **e**
control-plane. È il modo ufficiale di kind; nessun controller in-cluster può farlo.
`spec.upgrade` (sostituzione del nodo) vale solo su **OpenShift/Cluster API** reali.
```bash
kind delete cluster --name mo-demo
cat <<EOF | kind create cluster --name mo-demo --config -
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    image: kindest/node:v1.30.2     # <-- nuova versione
  - role: worker
    image: kindest/node:v1.30.2
  - role: worker
    image: kindest/node:v1.30.2
EOF
kubectl get nodes -o wide           # tutti alla nuova versione
```

---

## 12. Riferimento comandi

| Azione | Comando |
|---|---|
| Lista richieste | `kubectl get mreq` |
| Dettaglio | `kubectl get mreq <name> -o yaml` |
| Solo fase | `kubectl get mreq <name> -o jsonpath='{.status.phase}'` |
| Preflight | `kubectl get mreq <name> -o jsonpath='{range .status.preflight[*]}{.status}{" "}{.code}{"\n"}{end}'` |
| Per-nodo | `kubectl get mreq <name> -o jsonpath='{range .status.nodes[*]}{.node}{" "}{.phase}{"\n"}{end}'` |
| Approva drain | `kubectl patch mreq <name> --type=merge -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Approved","approvedBy":"me"}]}}}'` |
| Pausa / Annulla | `... -p '{"spec":{"pause":true}}'` · `... -p '{"spec":{"cancel":true}}'` |
| Log | `kubectl -n maintenance-orchestrator-system logs deploy/maintenance-orchestrator -f` |
| Metriche | `kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-metrics 8080:8080` |
| Dashboard | `kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-ui 8082:8082` |

---

## 13. Troubleshooting

| Sintomo | Causa & rimedio |
|---|---|
| Pod controller `ImagePullBackOff` | Immagine non caricata: `kind load docker-image maintenance-orchestrator:latest --name mo-demo`. |
| `mreq` resta in `Blocked` | Leggi `.status.preflight[]` per il `code` fallito (`CONTROL_PLANE_PROTECTED`, `TOO_MANY_UNAVAILABLE`, `INSUFFICIENT_CAPACITY`, `MACHINE_NOT_FOUND`). |
| Drain fermo `Blocked` con `BlockReason: PDB` | Un PDB nega l'eviction (corretto!). Aumenta le repliche o allenta il PDB. |
| Resta `AwaitingApproval` | Manca la decisione del gate — patcha `approval.gates[]`. |
| Si è interrotta la demo dopo un drain | Probabilmente hai drenato il nodo del controller. Targhetta un worker diverso (vedi §4). |

---

## 14. Note e pulizia

- **`Advisory`** è come `DryRun` ma **non termina**: rivaluta in continuo e tiene il piano
  aggiornato in `.status` — utile come cruscotto di rischio.
- **Force eviction** è off di default: richiede `spec.force: true` **e**
  `allowForceEviction: true` nella policy.
- Per OpenShift sostituisci `kubectl` con `oc`; i nodi in aggiornamento MCO sono `Skipped`.

```bash
kind delete cluster --name mo-demo
```
