# Installation guide — Maintenance Orchestrator

> 🇬🇧 English · 🇮🇹 Versione italiana: [`INSTALL.md`](INSTALL.md)
>
> 📍 **This is the ONE installation guide to follow.** The README only has a quickstart;
> `DEMO.en.md` shows usage examples and assumes you installed from here.

This guide documents **every step** of the installation, assuming nothing: exact
prerequisites, image build, image distribution to the cluster, install (3 methods),
verification, dashboard access, the full configuration reference, OpenShift, upgrade,
uninstall and troubleshooting.

For **simulation on Docker Desktop/kind** see [`DEMO.en.md`](DEMO.en.md); for the
**architecture** [`DESIGN.en.md`](DESIGN.en.md).

---

## 0. What gets installed

The product is **a single controller** (one Deployment) plus its CRDs and RBAC. No
external database: all state lives in Kubernetes objects (the CRs' `.status`).

| Object | Name | Scope | Notes |
|---|---|---|---|
| CRD | `maintenancerequests.maintenance.platform.dev` | Cluster | shortName `mreq` |
| CRD | `maintenancepolicies.maintenance.platform.dev` | Cluster | shortName `mpol` |
| Namespace | `maintenance-orchestrator-system` | — | everything else lives here |
| ServiceAccount | `maintenance-orchestrator` | ns | the controller's identity |
| ClusterRole + Binding | `maintenance-orchestrator` | Cluster | rights over nodes/pods/PDB/machines/CRs |
| Role + Binding | `maintenance-orchestrator-leader-election` | ns | leader-election lease |
| ConfigMap | `maintenance-orchestrator-config` | ns | mounted `config.yaml` |
| Deployment | `maintenance-orchestrator` | ns | 1 replica, non-root, read-only FS |
| Service | `maintenance-orchestrator-metrics` | ns | ClusterIP :8080 (Prometheus) |
| Service | `maintenance-orchestrator-ui` | ns | ClusterIP :8082 (dashboard) |
| MaintenancePolicy | `cluster-default` | Cluster | default guardrails (CR, you create it) |

**Container ports:** `8080` metrics · `8081` health (`/healthz`, `/readyz`) ·
`8082` web dashboard (when `uiEnabled`).

> ⚠️ CRDs and ClusterRole/Binding are **cluster-scoped**: installing them requires
> **cluster-admin** (or equivalent).

---

## 1. Prerequisites

### 1.1 Always required
- **A Kubernetes cluster** ≥ 1.27 (vanilla, EKS/GKE/AKS, OpenShift ≥ 4.12, kind, Docker
  Desktop). The controller uses only stable APIs (`core/v1`, `apps/v1`, `policy/v1`).
- **`kubectl`** ≥ 1.27 pointing at the target cluster (`kubectl config current-context`).
- **cluster-admin privileges** (for CRDs + ClusterRole). Check:
  ```bash
  kubectl auth can-i create customresourcedefinitions
  kubectl auth can-i create clusterroles
  # both must print: yes
  ```

### 1.2 Only to build the image
- **Docker** (or Podman) for `docker build`.
- *(alternative)* **Go ≥ 1.22** + **make** for the binary (`make build`) or the Makefile
  targets. Module: `github.com/Sindi98/maintenance-orchestrator`.

> If you already have a published image, **skip section 2** and go to section 3.

### 1.3 Quick check
```bash
kubectl version
kubectl config current-context
kubectl get nodes -o wide
docker version            # only if building the image
go version                # ditto
```

---

## 2. Get the source & build the image

```bash
git clone https://github.com/Sindi98/maintenance-orchestrator.git
cd maintenance-orchestrator
```

The `Dockerfile` produces a static binary (`CGO_ENABLED=0`, `-trimpath`, stripped) on
`gcr.io/distroless/static:nonroot` (user **uid 65532**, minimal FS) — runs unchanged under
the OpenShift `restricted-v2` SCC. The dashboard templates/CSS/JS are **embedded** in the
binary (`go:embed`): no Node/build step.

```bash
# Pick a tag. For Docker Desktop the default :latest is fine.
export IMG=maintenance-orchestrator:latest

# Makefile (runs 'docker build -t $IMG .')
make docker-build IMG=$IMG

# ...or plain docker
docker build -t "$IMG" .
```

**Multi-arch:** the `Dockerfile` honors `TARGETOS/TARGETARCH`. To target another cluster
architecture use buildx:
```bash
docker buildx build --platform linux/amd64,linux/arm64 -t registry.example.com/maintenance-orchestrator:v0.1.0 --push .
```

---

## 3. Make the image available to the cluster

The Deployment uses `imagePullPolicy: IfNotPresent` and the placeholder image
`maintenance-orchestrator:latest`. How you make it available depends on the cluster.

### 3.A Docker Desktop (built-in Kubernetes)
The cluster shares the Docker daemon: the freshly built image is **already visible**, no
push. Keep the `maintenance-orchestrator:latest` tag as-is.

### 3.B kind
```bash
kind load docker-image maintenance-orchestrator:latest --name <cluster>
```

### 3.C Real cluster (registry)
Tag and push to a registry the cluster can pull from; reference it in section 5.4.
```bash
docker tag maintenance-orchestrator:latest registry.example.com/maintenance-orchestrator:v0.1.0
docker push registry.example.com/maintenance-orchestrator:v0.1.0
```

### 3.D Private registry (imagePullSecret)
```bash
kubectl -n maintenance-orchestrator-system create secret docker-registry regcred \
  --docker-server=registry.example.com --docker-username=USER --docker-password=PASS
# then add to deploy/manager/deployment.yaml, under spec.template.spec:
#   imagePullSecrets:
#     - name: regcred
```

> **Air-gapped:** mirror the image into your internal registry (`skopeo copy` or
> `docker save`/`load`), then reference it.

---

## 4. (Real cluster only) Create the namespace & secrets

The step-by-step (5.2) and `make deploy` (5.3) paths create the namespace for you. If you
need it earlier (e.g. for the `regcred` secret above), create it first:
```bash
kubectl apply -f deploy/manager/namespace.yaml
```

---

## 5. Installation — pick ONE method

### 5.1 Method A — Kustomize (recommended)
Applies **CRDs + RBAC + manager** (namespace, configmap, deployment, metrics service, UI
service) at once. The default policy and samples are NOT included (they are CRs that need
the CRDs `Established` first).
```bash
kubectl apply -k deploy
kubectl apply -f deploy/samples/policy-cluster-default.yaml
```

### 5.2 Method B — step-by-step (explicit order)
Useful to understand/audit each resource. **Order matters**: CRDs must be `Established`
before creating CRs (the policy).
```bash
# 1) Namespace
kubectl apply -f deploy/manager/namespace.yaml

# 2) CRDs, then WAIT until they are Established
kubectl apply -f deploy/crd
kubectl wait --for=condition=Established --timeout=60s \
  crd/maintenancerequests.maintenance.platform.dev \
  crd/maintenancepolicies.maintenance.platform.dev

# 3) RBAC (ServiceAccount, ClusterRole/Binding, leader-election Role/Binding)
kubectl apply -f deploy/rbac

# 4) Default cluster policy (the name the controller expects via defaultPolicyName)
kubectl apply -f deploy/samples/policy-cluster-default.yaml

# 5) Config + controller + services
kubectl apply -f deploy/manager/configmap.yaml
kubectl apply -f deploy/manager/deployment.yaml
kubectl apply -f deploy/manager/service.yaml
kubectl apply -f deploy/manager/ui-service.yaml
```

### 5.3 Method C — Makefile
```bash
make deploy        # namespace, CRD, RBAC, configmap, deployment, service, ui-service
kubectl apply -f deploy/samples/policy-cluster-default.yaml   # the policy is created separately
```

### 5.4 Point the Deployment at your pushed image
**Real clusters only** (section 3.C): the manifest ships `:latest` as a placeholder. Set
it to your reference:
```bash
kubectl -n maintenance-orchestrator-system set image \
  deployment/maintenance-orchestrator \
  manager=registry.example.com/maintenance-orchestrator:v0.1.0
```

---

## 6. Verify the install

```bash
# 1) CRDs Established
kubectl get crd | grep maintenance.platform.dev

# 2) Controller pod Running & Ready (1/1)
kubectl -n maintenance-orchestrator-system get pods -o wide
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator --timeout=120s

# 3) Structured logs: look for "starting maintenance-orchestrator" + "successfully acquired lease"
kubectl -n maintenance-orchestrator-system logs deploy/maintenance-orchestrator | head -20

# 4) Leader election: an active Lease
kubectl -n maintenance-orchestrator-system get lease

# 5) Policy present
kubectl get mpol cluster-default

# 6) Health & metrics (port-forward 8081/8080)
kubectl -n maintenance-orchestrator-system port-forward deploy/maintenance-orchestrator 8081:8081 8080:8080 &
curl -fsS localhost:8081/healthz && echo " <- healthz ok"
curl -fsS localhost:8081/readyz  && echo " <- readyz ok"
curl -fsS localhost:8080/metrics | grep -E 'active_maintenances|maintenance_requests_total' | head
kill %1 2>/dev/null
```

All green = installed. If the pod is not `Running`, jump to section 15.

---

## 7. Access the web dashboard

The dashboard is on by default in the ConfigMap (`uiEnabled: true`, port `:8082`). It has
**no authentication**, so it is only exposed as a `ClusterIP`.
```bash
kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-ui 8082:8082
# open  ->  http://localhost:8082
```
In production put it behind an **authenticating ingress/route**, or disable it with
`uiEnabled: false` (section 10).

---

## 8. End-to-end smoke test (DryRun, non-destructive)

Confirms the controller actually reconciles. DryRun mutates nothing.
```bash
# A fake node just for the test (an API object, no VM)
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Node
metadata: { name: smoke-node-1, labels: { kubernetes.io/os: linux } }
status: { conditions: [ { type: Ready, status: "True" } ] }
EOF

cat <<EOF | kubectl apply -f -
apiVersion: maintenance.platform.dev/v1alpha1
kind: MaintenanceRequest
metadata: { name: smoke-test }
spec:
  mode: DryRun
  reason: "install smoke test"
  requestedBy: "installer"
  target: { type: Node, nodeNames: ["smoke-node-1"] }
  approval: { policy: AutoApprove }
EOF

# Expected: phase Completed with a plan in .status
kubectl get mreq smoke-test -o jsonpath='{.status.phase}{"  "}{.status.plan.totalNodes}{" node(s)\n"}'

# Cleanup
kubectl delete mreq smoke-test --ignore-not-found
kubectl delete node smoke-node-1 --ignore-not-found
```

---

## 9. Optional components

### 9.1 NetworkPolicy
Restricts ingress (8080/8081/8082) and egress (DNS + API server). Requires a CNI that
enforces NetworkPolicies (Calico, Cilium, OVN-Kubernetes).
```bash
kubectl apply -f deploy/manager/networkpolicy.yaml
```

### 9.2 ServiceMonitor (Prometheus Operator)
Requires the Prometheus Operator CRDs (`monitoring.coreos.com/v1`). On OpenShift enable
user-workload-monitoring first.
```bash
kubectl apply -f deploy/manager/servicemonitor.yaml
```
> They are not in `kubectl apply -k deploy` because they depend on external CRDs/CNI.

---

## 10. Configuration reference

Config is layered at startup: **built-in defaults → YAML file (`CONFIG_FILE`) →
environment variables**, then validated. The Deployment mounts the file from the ConfigMap
`maintenance-orchestrator-config` at `/etc/maintenance-orchestrator/config.yaml` via
`--config`.

| YAML key | Env var | Default | Meaning |
|---|---|---|---|
| `metricsAddr` | `METRICS_ADDR` | `:8080` | Prometheus metrics bind |
| `probeAddr` | `PROBE_ADDR` | `:8081` | `/healthz`, `/readyz` bind |
| `uiEnabled` | `UI_ENABLED` | `false`¹ | enable the web dashboard |
| `uiAddr` | `UI_ADDR` | `:8082` | dashboard bind |
| `leaderElection` | `LEADER_ELECTION` | `true` | single active instance |
| `leaderElectionID` | `LEADER_ELECTION_ID` | `maintenance-orchestrator.maintenance.platform.dev` | Lease name |
| `reconcileConcurrency` | `RECONCILE_CONCURRENCY` | `2` | concurrent reconciles per controller |
| `evictionPollInterval` | `EVICTION_POLL_INTERVAL` | `5s` | re-check of a draining node |
| `globalRequeueInterval` | `GLOBAL_REQUEUE_INTERVAL` | `30s` | steady-state requeue |
| `defaultDrainTimeout` | `DEFAULT_DRAIN_TIMEOUT` | `15m` | per-node drain timeout if unset in spec |
| `defaultGlobalTimeout` | `DEFAULT_GLOBAL_TIMEOUT` | `2h` | request global timeout if unset in spec |
| `defaultReplacementTimeout` | `DEFAULT_REPLACEMENT_TIMEOUT` | `20m` | wait for a replacement node (upgrade) |
| `logLevel` | `LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `logFormat` | `LOG_FORMAT` | `json` | `json\|console` |
| `enableK8sEvents` | `ENABLE_K8S_EVENTS` | `true` | emit Kubernetes Events |
| `defaultPolicyName` | `DEFAULT_POLICY_NAME` | `cluster-default` | policy used without `policyRef` |
| `auditExportPath` | `AUDIT_EXPORT_PATH` | _(empty)_ | JSON-lines audit file (writable volume) |
| `defaultPoolKeys` | `DEFAULT_POOL_KEYS` (CSV) | known pool labels (OCP/EKS/GKE/AKS/Karpenter) | node-label keys treated as pools |

¹ code default is `false`; the shipped ConfigMap sets it to `true`.

**To change config:** edit the ConfigMap and restart the controller.
```bash
kubectl -n maintenance-orchestrator-system edit configmap maintenance-orchestrator-config
kubectl -n maintenance-orchestrator-system rollout restart deploy/maintenance-orchestrator
```

---

## 11. What the RBAC grants

The `maintenance-orchestrator` ClusterRole is **minimal**: read/update its own CRs;
read/patch nodes (cordon/uncordon); read pods and create `pods/eviction` (honors PDBs);
`delete` pods only for policy-gated force eviction; read PDBs and workloads (`apps`);
create Events; `delete` `machines` (OpenShift/Cluster API) for node replacement. The
namespaced Role only manages the leader-election Lease.
```bash
kubectl describe clusterrole maintenance-orchestrator    # inspect the exact permissions
```

---

## 12. OpenShift

Same as above but with `oc`:
```bash
oc apply -k deploy
oc apply -f deploy/samples/policy-cluster-default.yaml
```
- **SCC**: the pod runs non-root (uid 65532), with no added capabilities,
  `seccompProfile: RuntimeDefault` and a read-only root FS → compatible with
  `restricted-v2` **without** a custom SCC.
- **Monitoring**: for scraping via user-workload-monitoring enable that monitoring and
  apply `deploy/manager/servicemonitor.yaml`.
- **MCO**: nodes mid Machine Config update are marked `Skipped`.
- **Replacement upgrade**: use `machine.openshift.io` as the Machine API and a policy with
  `allowNodeReplacement: true` (see `deploy/samples/mreq-pool-upgrade.yaml`).

---

## 13. Upgrading the operator

**Image-only** (no CRD change): bump the tag.
```bash
kubectl -n maintenance-orchestrator-system set image \
  deployment/maintenance-orchestrator manager=registry.example.com/maintenance-orchestrator:vNEXT
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator
```
**With new CRD fields**: apply the updated CRDs first, then the image. CRD changes are
additive/backward-compatible; existing CRs stay valid.
```bash
kubectl apply -f deploy/crd
kubectl apply -k deploy
```

---

## 14. Uninstall

> ⚠️ Deleting the CRDs **removes all** `MaintenanceRequest`/`MaintenancePolicy`. Finish or
> cancel active requests first.

```bash
# 1) (recommended) check active requests
kubectl get mreq

# 2) remove manager + RBAC (kustomize or make)
kubectl delete -k deploy --ignore-not-found     # removes crd, rbac, manager
#   ...or: make undeploy && make uninstall

# 3) remove remaining CRs and the namespace
kubectl delete mpol --all
kubectl delete namespace maintenance-orchestrator-system --ignore-not-found

# 4) remove the CRDs (if not already removed in step 2)
kubectl delete -f deploy/crd --ignore-not-found
```

---

## 15. Install troubleshooting

| Symptom | Cause & fix |
|---|---|
| `kubectl apply` on CRD/ClusterRole → `forbidden` | You are not cluster-admin (section 1.1). |
| Pod `ImagePullBackOff` / `ErrImageNeverPull` | Image not present. Docker Desktop: rebuild. kind: `kind load docker-image`. Real: push + `set image` (5.4) + maybe `imagePullSecret` (3.D). |
| Pod `CrashLoopBackOff`, log `invalid configuration` | Invalid config value (e.g. `logFormat`, durations ≤ 0, empty `uiAddr` with `uiEnabled`). Fix the ConfigMap, restart. |
| Pod `Running` but `0/1` Ready for long | `readyz` not passing: check logs; usually missing RBAC or unsynced cache. |
| `mpol cluster-default` missing / requests stay `Pending` | You skipped the policy: `kubectl apply -f deploy/samples/policy-cluster-default.yaml`. |
| `no matches for kind "MaintenanceRequest"` | CRDs not `Established` yet — run the `kubectl wait` from 5.2. |
| `kubectl apply -k deploy` kustomize error | kubectl too old, or `kubectl kustomize deploy \| kubectl apply -f -`. |
| Dashboard not reachable | `uiEnabled` false, missing port-forward, or NetworkPolicy blocking 8082. |
| Empty metrics | Check the 8080 port-forward and the `maintenance-orchestrator-metrics` service. |
| Multiple replicas but only one works | Expected: leader election keeps one active reconciler (the dashboard runs on all). |

---

## 16. Run locally (development)

For development you can run the controller **out-of-cluster** against the current
kubeconfig (CRDs must be installed: `make install`).
```bash
make install                      # apply the CRDs
kubectl apply -f deploy/samples/policy-cluster-default.yaml
make run                          # go run ./cmd/manager --config hack/config.local.yaml
```
The dashboard is already enabled in `hack/config.local.yaml` → `http://localhost:8082`.
