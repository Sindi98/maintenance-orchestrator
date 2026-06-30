# Quickstart & Demo — Maintenance Orchestrator

> 🇬🇧 English · 🇮🇹 Versione italiana: [`DEMO.md`](DEMO.md)

This guide shows **how to use** the Maintenance Orchestrator and **how to simulate it** on
Docker Desktop, both with the built-in single-node Kubernetes and with a multi-node `kind`
cluster (which runs on the same Docker engine as Docker Desktop).

For the **full installation** see [`INSTALL.en.md`](INSTALL.en.md).

---

## 0. 30-second mental model

The orchestrator is a **Kubernetes controller** driven by two CRDs:
- **`MaintenancePolicy`** (`mpol`): cluster guardrails (control-plane protection, max
  unavailable nodes, concurrency, failure threshold, windows, etc.).
- **`MaintenanceRequest`** (`mreq`): a single auditable node/pool maintenance operation.
  Each request flows through a **state machine**:
  `Pending → Validating → (AwaitingApproval) → Planned → Executing → Completed`
  (or `Blocked / Failed / Cancelled / Paused`).

**Modes:** `DryRun` (analyze, never mutates) · `Advisory` (continuously re-evaluates, never
mutates) · `Execute` (cordon → drain → uncordon).

---

## 1. Prerequisites

- **Docker Desktop** ≥ 4.x with **Kubernetes enabled** (Settings → Kubernetes → *Enable
  Kubernetes*).
- **`kubectl`** on PATH (Docker Desktop ships it).
- **Go** ≥ 1.22 and **make** (only to build the image from source).
- *(Path B only)* **`kind`** ≥ 0.20 (`brew install kind`, `choco install kind`, or see
  https://kind.sigs.k8s.io).

Verify:
```bash
kubectl version --client
kubectl config use-context docker-desktop
kubectl get nodes
```

---

## 2. Build the image

From the repo root, build the controller image. With Docker Desktop's Kubernetes you
**don't need to push**: the cluster shares the Docker daemon, so the local image is already
visible (the Deployment uses `imagePullPolicy: IfNotPresent`).

```bash
# Option A — Makefile
make docker-build IMG=maintenance-orchestrator:latest

# Option B — plain docker
docker build -t maintenance-orchestrator:latest .
```

---

## 3. Path A — Docker Desktop Kubernetes (single-node, safe)

The built-in cluster has **a single node** (`docker-desktop`) which is **control-plane**,
hence protected by the guardrails. It's ideal to demonstrate **DryRun / Advisory**,
**preflight**, **planning**, **risk score** and the **safety guardrails** — with zero real
disruption. (An `Execute` against that node is deliberately **blocked**: that's the safety
working as intended.)

### 3.1 Install CRDs, RBAC, policy and controller

```bash
# All at once (CRDs + RBAC + namespace + manager)
kubectl apply -k deploy

# Create the default policy the controller expects
kubectl apply -f deploy/samples/policy-cluster-default.yaml

# Wait for the controller to be ready
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator
```

> If the image isn't picked up, force it:
> ```bash
> kubectl -n maintenance-orchestrator-system set image \
>   deploy/maintenance-orchestrator manager=maintenance-orchestrator:latest
> ```

### 3.2 Create fake nodes for the DryRun

The `node-dryrun` sample targets `worker-1`/`worker-2`, which don't exist on Docker
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

### 3.3 Run a DryRun and read the plan

```bash
kubectl apply -f deploy/samples/mreq-node-dryrun.yaml

# Short status
kubectl get mreq node-dryrun

# Detail: phase, preflight, plan, risk score
kubectl get mreq node-dryrun -o yaml | less
```

What to look at under `.status`: `phase: Completed`, the `preflight[]` array (with
`status: Pass/Warn/Fail`, `code`, `message`), and `plan` (with `batches`, `riskScore`,
`impact`). In DryRun **nothing is mutated**.

```bash
# Extract just the interesting parts
kubectl get mreq node-dryrun -o jsonpath='{.status.phase}{"\n"}'
kubectl get mreq node-dryrun -o jsonpath='{range .status.preflight[*]}{.status}{"\t"}{.code}{"\t"}{.message}{"\n"}{end}'
kubectl get mreq node-dryrun -o jsonpath='{.status.plan.riskScore}{"\n"}{.status.plan.riskFactors}{"\n"}'
```

### 3.4 Demonstrate the control-plane guardrail

Let's try an `Execute` on the real `docker-desktop` node: it must end up **Blocked** by
control-plane protection.

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
# Expected: Blocked   blocked by failed preflight checks (CONTROL_PLANE_PROTECTED)
```

### 3.5 Metrics and logs

```bash
# Structured JSON logs
kubectl -n maintenance-orchestrator-system logs deploy/maintenance-orchestrator -f

# Prometheus metrics (port-forward)
kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-metrics 8080:8080 &
curl -s localhost:8080/metrics | grep -E 'maintenance_(requests|success|failure)|preflight_failures|blocked_drains|active_maintenances'
```

### 3.6 Web dashboard

The built-in dashboard is on by default in the ConfigMap (`uiEnabled: true`). Port-forward
it and open the browser — live request list, preflight/plan/per-node detail, create
requests, and approve/pause/cancel.

```bash
kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-ui 8082:8082
# open  ->  http://localhost:8082
```
> ⚠️ No authentication: `ClusterIP` + port-forward only (or an authenticating ingress).

### 3.7 Partial cleanup

```bash
kubectl delete mreq node-dryrun try-controlplane --ignore-not-found
kubectl delete node worker-1 worker-2 --ignore-not-found
```

---

## 4. Path B — multi-node `kind`: a real Execute

To **actually** watch cordon → drain → uncordon across nodes you need a multi-node cluster.
`kind` creates one using Docker Desktop's Docker: no extra VMs.

### 4.1 Create the cluster

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

### 4.2 Load the image into the kind cluster

`kind` has its own internal registry: the local image must be **loaded** (having it in
Docker Desktop is not enough).

```bash
kind load docker-image maintenance-orchestrator:latest --name mo-demo
```

### 4.3 Install and prepare a workload with a PDB

```bash
kubectl apply -k deploy
kubectl apply -f deploy/samples/policy-cluster-default.yaml
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator

# Label the workers as a demo "pool"
for n in $(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o name); do
  kubectl label "$n" pool=demo --overwrite
done

# 3-replica app + PDB (minAvailable 2) to watch the PDB being honored
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
> kubectl get pods -A -o wide --field-selector spec.nodeName=mo-demo-worker
> ```

### 4.4 DryRun, then Execute on one node

Replace `mo-demo-worker2` with a worker **different** from the controller's.

```bash
# --- DryRun: analyze without mutating
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

# --- Execute: flip the SAME request to Execute
kubectl patch mreq drain-one --type=merge -p '{"spec":{"mode":"Execute"}}'

# Watch phase, summary and per-node
watch -n2 "kubectl get mreq drain-one -o jsonpath='{.status.phase}{\" \"}{.status.summary}{\"\n\"}'; \
  echo '--- nodes ---'; kubectl get mreq drain-one -o jsonpath='{range .status.nodes[*]}{.node}{\"\t\"}{.phase}{\"\t\"}{.message}{\"\n\"}{end}'"
```

In parallel, watch the node go `SchedulingDisabled` and pods reschedule (the
`minAvailable: 2` PDB is honored by the eviction API):

```bash
kubectl get nodes
kubectl get pods -o wide -w
```

When done: `phase: Completed`, the node is **uncordoned** (`uncordonAfter: true`) and
returns `Ready,SchedulingEnabled`.

### 4.5 Pool maintenance with manual approval

Rolling maintenance of all `pool=demo` workers, **one node at a time**, with **manual
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

# It stops at AwaitingApproval
kubectl get mreq pool-demo -o jsonpath='{.status.phase}{"\t"}{.status.approvalGate}{"\n"}'

# APPROVE the drain gate
kubectl patch mreq pool-demo --type=merge \
  -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Approved","approvedBy":"sre@example.com"}]}}}'

# (alternatively) REJECT  -> the request becomes Cancelled
# kubectl patch mreq pool-demo --type=merge \
#   -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Rejected","approvedBy":"sre@example.com"}]}}}'
```

### 4.6 Runtime control: pause / resume / cancel

```bash
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"pause":true}}'    # hold
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"pause":false}}'   # resume
kubectl patch mreq pool-demo --type=merge -p '{"spec":{"cancel":true}}'   # cancel
```

### 4.7 Upgrade the Kubernetes version (node replacement)

With `spec.upgrade` the maintenance **replaces** the node after draining: it deletes its
`Machine` (OpenShift `machine.openshift.io` or Cluster API `cluster.x-k8s.io`) so the pool
recreates it at the template's version. Requires `allowNodeReplacement: true` in the policy
(already set in the `cluster-default` sample).

> ⚠️ **`kind` has no Machine API**, so preflight returns `MACHINE_NOT_FOUND` and the node
> goes `Blocked`; try replacement on real **OpenShift** or **Cluster API**. See
> [`../deploy/samples/mreq-pool-upgrade.yaml`](../deploy/samples/mreq-pool-upgrade.yaml).

```bash
# On a cluster with a Machine API
kubectl apply -f deploy/samples/mreq-pool-upgrade.yaml
kubectl get mreq pool-upgrade -o jsonpath='{range .status.nodes[*]}{.node}{"  "}{.phase}{"  "}{.message}{"\n"}{end}'
# Expected phases: ... Draining -> Replacing -> AwaitingReplacement -> Completed
```

### 4.8 Cleanup

```bash
kind delete cluster --name mo-demo
```

---

## 5. Command cheat-sheet

| Action | Command |
|---|---|
| List requests | `kubectl get mreq` |
| Detail | `kubectl get mreq <name> -o yaml` |
| Phase only | `kubectl get mreq <name> -o jsonpath='{.status.phase}'` |
| Preflight | `kubectl get mreq <name> -o jsonpath='{range .status.preflight[*]}{.status}{" "}{.code}{"\n"}{end}'` |
| Plan | `kubectl get mreq <name> -o jsonpath='{.status.plan}'` |
| Per-node | `kubectl get mreq <name> -o jsonpath='{range .status.nodes[*]}{.node}{" "}{.phase}{"\n"}{end}'` |
| Policy | `kubectl get mpol cluster-default -o yaml` |
| Approve drain | `kubectl patch mreq <name> --type=merge -p '{"spec":{"approval":{"gates":[{"gate":"Drain","decision":"Approved","approvedBy":"me"}]}}}'` |
| Pause | `kubectl patch mreq <name> --type=merge -p '{"spec":{"pause":true}}'` |
| Cancel | `kubectl patch mreq <name> --type=merge -p '{"spec":{"cancel":true}}'` |
| Logs | `kubectl -n maintenance-orchestrator-system logs deploy/maintenance-orchestrator -f` |
| Metrics | `kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-metrics 8080:8080` |
| Dashboard | `kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-ui 8082:8082` |

---

## 6. Troubleshooting

| Symptom | Cause & fix |
|---|---|
| Controller pod `ImagePullBackOff` | Image not in the cluster. Docker Desktop: rebuild + `set image`. kind: `kind load docker-image maintenance-orchestrator:latest --name mo-demo`. |
| `mreq` stuck in `Validating`/`Blocked` | Read `.status.preflight[]` for the failing `code` (e.g. `CONTROL_PLANE_PROTECTED`, `TOO_MANY_UNAVAILABLE`, `INSUFFICIENT_CAPACITY`). |
| `Execute` on control-plane won't start | Intended: protection is on. Override only in tests: `spec.allowControlPlane: true` **and** a policy with `protectControlPlane: false`. |
| Drain stuck `Blocked` with `BlockReason: PDB` | A PDB denies eviction (correct!). Add replicas or relax the PDB. |
| Stays `AwaitingApproval` | The gate decision is missing — patch `approval.gates[]`. |
| `kubectl apply -k deploy` error | Update kubectl (built-in kustomize) or use `kubectl kustomize deploy \| kubectl apply -f -`. |
| Empty metrics | Check the 8080 port-forward and the service name. |

---

## 7. Important notes

- **Single-node = no real drain.** On Docker Desktop use `DryRun`/`Advisory`; for a real
  `Execute` use multi-node `kind` (Path B).
- **`Advisory`** is like `DryRun` but **never finishes**: it keeps re-evaluating and
  refreshing the plan in `.status` — handy as a risk dashboard.
- **Force eviction** is off by default: it needs both `spec.force: true` and policy
  `allowForceEviction: true`.
- On OpenShift use `oc` instead of `kubectl`; nodes mid-MCO-update are marked `Skipped`.
