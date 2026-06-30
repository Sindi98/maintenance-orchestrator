# Demo & usage — Maintenance Orchestrator

> 🇬🇧 English · 🇮🇹 Versione italiana: [`DEMO.md`](DEMO.md)
>
> 📍 **To INSTALL, follow only [`INSTALL.en.md`](INSTALL.en.md).** This guide shows **how to
> use** the orchestrator on a **multi-node** `kind` cluster — the only environment where you
> actually see `cordon → drain → uncordon`. No single-node examples: on one node (the
> control-plane) the guardrails block everything by design and nothing real happens.

---

## 0. 30-second mental model

The orchestrator is a **Kubernetes controller** driven by two CRDs:
- **`MaintenancePolicy`** (`mpol`): cluster guardrails (control-plane protection, max
  unavailable nodes, concurrency, failure threshold, windows, etc.).
- **`MaintenanceRequest`** (`mreq`): a single auditable maintenance operation. Each request
  flows through a **state machine**:
  `Pending → Validating → (AwaitingApproval) → Planned → Executing → Completed`
  (or `Blocked / Failed / Cancelled / Paused`).

**Modes:** `DryRun` (analyze, never mutates) · `Advisory` (continuously re-evaluates, never
mutates) · `Execute` (cordon → drain → uncordon).

---

## 1. Prerequisites

- **`kind`** ≥ 0.20 (`brew install kind`, `choco install kind`, or https://kind.sigs.k8s.io).
- **Docker** running (kind creates the nodes as containers).
- **`kubectl`** on PATH.
- **Go** ≥ 1.22 and **make** only to build the image from source.

```bash
kind version && docker version --format '{{.Server.Version}}' && kubectl version --client
```

---

## 2. Create a multi-node kind cluster

1 control-plane + 3 workers (the minimum to see real drain/uncordon):
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

---

## 3. Build and load the image

`kind` has an internal registry: the local image must be **loaded** (having it in Docker is
not enough).
```bash
docker build -t maintenance-orchestrator:latest .
kind load docker-image maintenance-orchestrator:latest --name mo-demo
```

---

## 4. Install and prepare a workload with a PDB

```bash
kubectl apply -k deploy
kubectl apply -f deploy/samples/policy-cluster-default.yaml
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator

# Label the workers as a demo "pool"
for n in $(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o name); do
  kubectl label "$n" pool=demo --overwrite
done

# 3-replica app + PDB (minAvailable 2) to watch the PDB being honored during the drain
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

> ⚠️ **Tip:** don't drain the worker hosting the controller, or you'll interrupt the demo.
> Check where it runs and target a different node.
> ```bash
> kubectl -n maintenance-orchestrator-system get pod -o wide   # note the NODE
> ```

---

## 5. DryRun: analyze a node (no mutation)

Replace `mo-demo-worker2` with a worker **different** from the controller's.
```bash
cat <<EOF | kubectl apply -f -
apiVersion: maintenance.platform.dev/v1alpha1
kind: MaintenanceRequest
metadata: { name: drain-one }
spec:
  mode: DryRun
  reason: "pre-maintenance analysis"
  requestedBy: "you@example.com"
  target: { type: Node, nodeNames: ["mo-demo-worker2"] }
  strategy: Serial
  maxConcurrent: 1
  uncordonAfter: true
  approval: { policy: AutoApprove }
EOF

# .status: phase Completed, preflight[], plan (risk score, impact). No mutation.
kubectl get mreq drain-one -o jsonpath='{.status.phase}{"\n"}'
kubectl get mreq drain-one -o jsonpath='{range .status.preflight[*]}{.status}{"\t"}{.code}{"\t"}{.message}{"\n"}{end}'
kubectl get mreq drain-one -o jsonpath='riskScore={.status.plan.riskScore}{"\n"}impact={.status.plan.impact}{"\n"}'
```

---

## 6. Execute: a real node drain

Flip the **same** request to `Execute` and watch progress:
```bash
kubectl patch mreq drain-one --type=merge -p '{"spec":{"mode":"Execute"}}'

watch -n2 "kubectl get mreq drain-one -o jsonpath='{.status.phase}{\" \"}{.status.summary}{\"\n\"}'; \
  echo '--- nodes ---'; kubectl get mreq drain-one -o jsonpath='{range .status.nodes[*]}{.node}{\"\t\"}{.phase}{\"\t\"}{.message}{\"\n\"}{end}'"
```
In parallel, watch the node go `SchedulingDisabled` and pods reschedule (the
`minAvailable: 2` PDB is honored by the eviction API):
```bash
kubectl get nodes
kubectl get pods -o wide -w
```
When done: `phase: Completed`, the node is **uncordoned** and returns `Ready,SchedulingEnabled`.

---

## 7. Control-plane guardrail (must block)

An `Execute` on the control-plane must end **Blocked** by protection — that's the safety:
```bash
CP=$(kubectl get nodes -l node-role.kubernetes.io/control-plane -o jsonpath='{.items[0].metadata.name}')
cat <<EOF | kubectl apply -f -
apiVersion: maintenance.platform.dev/v1alpha1
kind: MaintenanceRequest
metadata: { name: try-controlplane }
spec:
  mode: Execute
  reason: "demo: control-plane protection"
  requestedBy: "you@example.com"
  target: { type: Node, nodeNames: ["$CP"] }
  approval: { policy: AutoApprove }
EOF
kubectl get mreq try-controlplane -o jsonpath='{.status.phase}{"\t"}{.status.message}{"\n"}'
# Expected: Blocked  (preflight CONTROL_PLANE_PROTECTED)
```

---

## 8. Pool maintenance with manual approval

Rolling over all `pool=demo` workers, **one node at a time**, with **manual approval**:
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

# Stops at AwaitingApproval
kubectl get mreq pool-demo -o jsonpath='{.status.phase}{"\t"}{.status.approvalGate}{"\n"}'

# APPROVE the drain gate
kubectl patch mreq pool-demo --type=merge \
  -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Approved","approvedBy":"sre@example.com"}]}}}'

# (alternatively) REJECT  -> the request becomes Cancelled
# kubectl patch mreq pool-demo --type=merge \
#   -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Rejected","approvedBy":"sre@example.com"}]}}}'
```

---

## 9. Runtime control: pause / resume / cancel

```bash
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"pause":true}}'    # hold
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"pause":false}}'   # resume
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"cancel":true}}'   # cancel
```

---

## 10. Web dashboard

```bash
kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-ui 8082:8082
# open  ->  http://localhost:8082   (live list, detail, create requests, approve/pause/cancel)
```
> ⚠️ No authentication: `ClusterIP` + port-forward only (or an authenticating ingress).

---

## 11. Upgrade the Kubernetes version of the nodes

On `kind` you do **not** use `spec.upgrade` (kind has no Machine API and no in-place
upgrade): you **recreate** the cluster with a newer node image — it upgrades workers **and**
control-plane. That's kind's official way; no in-cluster controller can do it. `spec.upgrade`
(node replacement) only applies to real **OpenShift/Cluster API** clusters.
```bash
kind delete cluster --name mo-demo
cat <<EOF | kind create cluster --name mo-demo --config -
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    image: kindest/node:v1.30.2     # <-- new version
  - role: worker
    image: kindest/node:v1.30.2
  - role: worker
    image: kindest/node:v1.30.2
EOF
kubectl get nodes -o wide           # all on the new version
```

---

## 12. Command cheat-sheet

| Action | Command |
|---|---|
| List requests | `kubectl get mreq` |
| Detail | `kubectl get mreq <name> -o yaml` |
| Phase only | `kubectl get mreq <name> -o jsonpath='{.status.phase}'` |
| Preflight | `kubectl get mreq <name> -o jsonpath='{range .status.preflight[*]}{.status}{" "}{.code}{"\n"}{end}'` |
| Per-node | `kubectl get mreq <name> -o jsonpath='{range .status.nodes[*]}{.node}{" "}{.phase}{"\n"}{end}'` |
| Approve drain | `kubectl patch mreq <name> --type=merge -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Approved","approvedBy":"me"}]}}}'` |
| Pause / Cancel | `... -p '{"spec":{"pause":true}}'` · `... -p '{"spec":{"cancel":true}}'` |
| Logs | `kubectl -n maintenance-orchestrator-system logs deploy/maintenance-orchestrator -f` |
| Metrics | `kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-metrics 8080:8080` |
| Dashboard | `kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-ui 8082:8082` |

---

## 13. Troubleshooting

| Symptom | Cause & fix |
|---|---|
| Controller pod `ImagePullBackOff` | Image not loaded: `kind load docker-image maintenance-orchestrator:latest --name mo-demo`. |
| `mreq` stuck `Blocked` | Read `.status.preflight[]` for the failing `code` (`CONTROL_PLANE_PROTECTED`, `TOO_MANY_UNAVAILABLE`, `INSUFFICIENT_CAPACITY`, `MACHINE_NOT_FOUND`). |
| Drain stuck `Blocked` with `BlockReason: PDB` | A PDB denies eviction (correct!). Add replicas or relax the PDB. |
| Stays `AwaitingApproval` | The gate decision is missing — patch `approval.gates[]`. |
| The demo got interrupted after a drain | You probably drained the controller's node. Target a different worker (see §4). |

---

## 14. Notes & cleanup

- **`Advisory`** is like `DryRun` but **never finishes**: it keeps re-evaluating and
  refreshing the plan in `.status` — handy as a risk dashboard.
- **Force eviction** is off by default: needs both `spec.force: true` and policy
  `allowForceEviction: true`.
- On OpenShift use `oc` instead of `kubectl`; nodes mid-MCO-update are marked `Skipped`.

```bash
kind delete cluster --name mo-demo
```
