# Installation guide — Maintenance Orchestrator
### Guida d'installazione 🇮🇹 / 🇬🇧 — completa e precisa

> 🇮🇹 Questa guida documenta **ogni passo** dell'installazione, senza dare nulla per
> scontato: prerequisiti esatti, build dell'immagine, distribuzione dell'immagine al
> cluster, installazione (3 metodi), verifica, accesso alla dashboard, riferimento di
> configurazione completo, OpenShift, upgrade, disinstallazione e troubleshooting.
>
> 🇬🇧 This guide documents **every step** of the installation, assuming nothing: exact
> prerequisites, image build, image distribution to the cluster, install (3 methods),
> verification, dashboard access, the full configuration reference, OpenShift, upgrade,
> uninstall and troubleshooting.

Per la **simulazione su Docker Desktop/kind** vedi anche [`DEMO.md`](DEMO.md); per
l'**architettura** [`DESIGN.md`](DESIGN.md) / [`DESIGN.en.md`](DESIGN.en.md).

---

## 0. Cosa viene installato / What gets installed

🇮🇹 Il prodotto è **un solo controller** (un Deployment) più i suoi CRD e RBAC. Non usa
database esterni: lo stato vive negli oggetti Kubernetes (`.status` dei CR).

🇬🇧 The product is **a single controller** (one Deployment) plus its CRDs and RBAC. No
external database: all state lives in Kubernetes objects (the CRs' `.status`).

| Oggetto / Object | Nome / Name | Scope | Note |
|---|---|---|---|
| CRD | `maintenancerequests.maintenance.platform.dev` | Cluster | shortName `mreq` |
| CRD | `maintenancepolicies.maintenance.platform.dev` | Cluster | shortName `mpol` |
| Namespace | `maintenance-orchestrator-system` | — | tutto il resto vive qui |
| ServiceAccount | `maintenance-orchestrator` | ns | identità del controller |
| ClusterRole + Binding | `maintenance-orchestrator` | Cluster | permessi su nodi/pod/PDB/machines/CR |
| Role + Binding | `maintenance-orchestrator-leader-election` | ns | lease di leader election |
| ConfigMap | `maintenance-orchestrator-config` | ns | file `config.yaml` montato |
| Deployment | `maintenance-orchestrator` | ns | 1 replica, non-root, FS read-only |
| Service | `maintenance-orchestrator-metrics` | ns | ClusterIP :8080 (Prometheus) |
| Service | `maintenance-orchestrator-ui` | ns | ClusterIP :8082 (dashboard) |
| MaintenancePolicy | `cluster-default` | Cluster | guardrail di default (CR, da creare) |

**Porte del container / container ports:** `8080` metrics · `8081` health
(`/healthz`, `/readyz`) · `8082` web dashboard (se `uiEnabled`).

> ⚠️ 🇮🇹 CRD e ClusterRole/Binding sono **cluster-scoped**: installarli richiede privilegi
> di **cluster-admin** (o equivalente). 🇬🇧 CRDs and ClusterRole/Binding are
> **cluster-scoped**: installing them requires **cluster-admin** (or equivalent).

---

## 1. Prerequisiti / Prerequisites

### 1.1 Sempre necessari / Always required
- **Un cluster Kubernetes** ≥ 1.27 (vanilla, EKS/GKE/AKS, OpenShift ≥ 4.12, kind, Docker
  Desktop). Il controller usa solo API stabili (`core/v1`, `apps/v1`, `policy/v1`).
- **`kubectl`** ≥ 1.27 configurato sul cluster target (`kubectl config current-context`).
- **Privilegi cluster-admin** sul cluster (per CRD + ClusterRole). Verifica:
  ```bash
  kubectl auth can-i create customresourcedefinitions
  kubectl auth can-i create clusterroles
  # entrambi devono stampare: yes
  ```

### 1.2 Necessari solo per costruire l'immagine / Only to build the image
- **Docker** (o Podman) per `docker build`.
- *(alternativa)* **Go ≥ 1.22** + **make** per il binario (`make build`) o per usare i
  target del Makefile. Modulo: `github.com/Sindi98/maintenance-orchestrator`.

> 🇮🇹 Se hai già un'immagine pubblicata, **salta la sezione 2** e vai alla 3.
> 🇬🇧 If you already have a published image, **skip section 2** and go to section 3.

### 1.3 Verifica rapida / Quick check
```bash
kubectl version
kubectl config current-context
kubectl get nodes -o wide
docker version            # solo se costruisci l'immagine / only if building
go version                # idem
```

---

## 2. Ottieni il sorgente e costruisci l'immagine / Get the source & build the image

```bash
git clone https://github.com/Sindi98/maintenance-orchestrator.git
cd maintenance-orchestrator
```

🇮🇹 Costruisci l'immagine del controller. Il `Dockerfile` produce un binario statico
(`CGO_ENABLED=0`, `-trimpath`, strip) su `gcr.io/distroless/static:nonroot` (utente
**uid 65532**, FS minimale) — compatibile con la SCC OpenShift `restricted-v2` senza
personalizzazioni. I template/CSS/JS della dashboard sono **embeddati** nel binario
(`go:embed`): nessun passo Node/build.

🇬🇧 Build the controller image. The `Dockerfile` produces a static binary on
distroless-nonroot (uid 65532); the dashboard assets are embedded in the binary
(`go:embed`), no Node/build step.

```bash
# IT: scegli un tag. Per Docker Desktop va bene anche il default :latest.
# EN: pick a tag. For Docker Desktop the default :latest is fine.
export IMG=maintenance-orchestrator:latest

# Makefile (esegue 'docker build -t $IMG .')
make docker-build IMG=$IMG

# ...oppure docker diretto / ...or plain docker
docker build -t "$IMG" .
```

🇮🇹 **Multi-arch:** il `Dockerfile` rispetta `TARGETOS/TARGETARCH`. Per un'immagine per
un cluster di architettura diversa usa buildx:
🇬🇧 **Multi-arch:** use buildx to target another cluster architecture:
```bash
docker buildx build --platform linux/amd64,linux/arm64 -t registry.example.com/maintenance-orchestrator:v0.1.0 --push .
```

---

## 3. Rendi l'immagine disponibile al cluster / Make the image available to the cluster

🇮🇹 Il Deployment usa `imagePullPolicy: IfNotPresent` e l'immagine placeholder
`maintenance-orchestrator:latest`. Il modo di renderla disponibile dipende dal cluster.

🇬🇧 The Deployment uses `imagePullPolicy: IfNotPresent` and the placeholder image
`maintenance-orchestrator:latest`. How you make it available depends on the cluster.

### 3.A Docker Desktop (Kubernetes integrato)
🇮🇹 Il cluster condivide il daemon Docker: l'immagine appena costruita è **già visibile**,
nessun push. Usa il tag `maintenance-orchestrator:latest` così com'è.
🇬🇧 The cluster shares the Docker daemon: the freshly built image is **already visible**,
no push. Keep the `:latest` tag.

### 3.B kind
```bash
kind load docker-image maintenance-orchestrator:latest --name <cluster>
```

### 3.C Cluster reale (registry) / Real cluster (registry)
🇮🇹 Tagga e pusha su un registry raggiungibile dal cluster, poi userai quel riferimento
nella sezione 5.3.
🇬🇧 Tag and push to a registry the cluster can pull from; reference it in section 5.3.
```bash
docker tag maintenance-orchestrator:latest registry.example.com/maintenance-orchestrator:v0.1.0
docker push registry.example.com/maintenance-orchestrator:v0.1.0
```

### 3.D Registry privato / Private registry (imagePullSecret)
```bash
kubectl -n maintenance-orchestrator-system create secret docker-registry regcred \
  --docker-server=registry.example.com --docker-username=USER --docker-password=PASS
# poi aggiungi a deploy/manager/deployment.yaml, sotto spec.template.spec:
#   imagePullSecrets:
#     - name: regcred
```

> 🇮🇹 **Air-gapped:** porta l'immagine nel registry interno con
> `docker save | docker load` o `skopeo copy`, poi usa quel riferimento.
> 🇬🇧 **Air-gapped:** mirror the image into your internal registry (`skopeo copy` or
> `docker save`/`load`), then reference it.

---

## 4. (Solo cluster reale) Crea il namespace e i secret / (Real cluster only) namespace & secrets

🇮🇹 Se usi il metodo step-by-step (5.2) o `make deploy` (5.3) il namespace viene creato
da loro. Se ti serve prima (es. per il `regcred` di sopra), crealo a mano:
🇬🇧 The step-by-step (5.2) and `make deploy` (5.3) paths create the namespace for you. If
you need it earlier (e.g. for the `regcred` secret above), create it first:
```bash
kubectl apply -f deploy/manager/namespace.yaml
```

---

## 5. Installazione / Installation — scegli UN metodo / pick ONE method

### 5.1 Metodo A — Kustomize (consigliato) / Method A — Kustomize (recommended)
🇮🇹 Applica in un colpo **CRD + RBAC + manager** (namespace, configmap, deployment,
service metriche, service UI). La policy di default e i sample NON sono inclusi (sono CR
che richiedono i CRD già `Established`).
🇬🇧 Applies **CRDs + RBAC + manager** (namespace, configmap, deployment, metrics service,
UI service) at once. The default policy and samples are NOT included (they are CRs that
need the CRDs `Established` first).
```bash
kubectl apply -k deploy
kubectl apply -f deploy/samples/policy-cluster-default.yaml
```

### 5.2 Metodo B — Passo-passo (ordine esplicito) / Method B — step-by-step (explicit order)
🇮🇹 Utile per capire/auditare ogni risorsa. **L'ordine conta**: i CRD devono essere
`Established` prima di creare CR (la policy).
🇬🇧 Useful to understand/audit each resource. **Order matters**: CRDs must be
`Established` before creating CRs (the policy).
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

# 4) Default cluster policy (the name controller expects via defaultPolicyName)
kubectl apply -f deploy/samples/policy-cluster-default.yaml

# 5) Config + controller + services
kubectl apply -f deploy/manager/configmap.yaml
kubectl apply -f deploy/manager/deployment.yaml
kubectl apply -f deploy/manager/service.yaml
kubectl apply -f deploy/manager/ui-service.yaml
```

### 5.3 Metodo C — Makefile
```bash
make deploy        # namespace, CRD, RBAC, configmap, deployment, service, ui-service
kubectl apply -f deploy/samples/policy-cluster-default.yaml   # la policy va creata a parte
```

### 5.4 Imposta l'immagine pushata / Point the Deployment at your pushed image
🇮🇹 **Solo cluster reali** (sezione 3.C): il manifest usa `:latest` come placeholder.
Sostituiscila col tuo riferimento:
🇬🇧 **Real clusters only** (section 3.C): the manifest ships `:latest` as a placeholder.
Set it to your reference:
```bash
kubectl -n maintenance-orchestrator-system set image \
  deployment/maintenance-orchestrator \
  manager=registry.example.com/maintenance-orchestrator:v0.1.0
```

---

## 6. Verifica l'installazione / Verify the install

```bash
# 1) CRD Established
kubectl get crd | grep maintenance.platform.dev

# 2) Pod del controller Running & Ready (1/1)
kubectl -n maintenance-orchestrator-system get pods -o wide
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator --timeout=120s

# 3) Log strutturati: cerca "starting maintenance-orchestrator" + "successfully acquired lease"
kubectl -n maintenance-orchestrator-system logs deploy/maintenance-orchestrator | head -20

# 4) Leader election: una Lease attiva
kubectl -n maintenance-orchestrator-system get lease

# 5) Policy presente
kubectl get mpol cluster-default

# 6) Health & metrics (port-forward su 8081/8080)
kubectl -n maintenance-orchestrator-system port-forward deploy/maintenance-orchestrator 8081:8081 8080:8080 &
curl -fsS localhost:8081/healthz && echo " <- healthz ok"
curl -fsS localhost:8081/readyz  && echo " <- readyz ok"
curl -fsS localhost:8080/metrics | grep -E 'active_maintenances|maintenance_requests_total' | head
kill %1 2>/dev/null
```

🇮🇹 Tutto verde = installato. Se il pod non è `Running`, salta alla sezione 12.
🇬🇧 All green = installed. If the pod is not `Running`, jump to section 12.

---

## 7. Accedi alla dashboard web / Access the web dashboard

🇮🇹 La dashboard è abilitata di default nel ConfigMap (`uiEnabled: true`, porta `:8082`).
**Non ha autenticazione**, perciò è esposta solo come `ClusterIP`.
🇬🇧 The dashboard is on by default in the ConfigMap (`uiEnabled: true`, port `:8082`). It
has **no authentication**, so it is only exposed as a `ClusterIP`.
```bash
kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-ui 8082:8082
# apri / open  ->  http://localhost:8082
```
🇮🇹 In produzione mettila dietro un **ingress/route con autenticazione**, oppure
disattivala con `uiEnabled: false` (sezione 10). 🇬🇧 In production put it behind an
**authenticating ingress/route**, or disable it with `uiEnabled: false` (section 10).

---

## 8. Smoke test end-to-end (DryRun, non distruttivo / non-destructive)

🇮🇹 Conferma che il controller riconcilia davvero. Il DryRun non modifica nulla.
🇬🇧 Confirms the controller actually reconciles. DryRun mutates nothing.
```bash
# IT: nodo fittizio solo per il test (oggetto API, nessuna VM) / EN: a fake node for the test
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

# Attesa: phase Completed con un plan in .status
kubectl get mreq smoke-test -o jsonpath='{.status.phase}{"  "}{.status.plan.totalNodes}{" node(s)\n"}'

# Pulizia / cleanup
kubectl delete mreq smoke-test --ignore-not-found
kubectl delete node smoke-node-1 --ignore-not-found
```

---

## 9. Componenti opzionali / Optional components

### 9.1 NetworkPolicy
🇮🇹 Limita ingress (8080/8081/8082) ed egress (DNS + API server). Richiede un CNI che
applichi le NetworkPolicy (Calico, Cilium, OVN-Kubernetes). 🇬🇧 Restricts ingress/egress;
requires a policy-enforcing CNI.
```bash
kubectl apply -f deploy/manager/networkpolicy.yaml
```

### 9.2 ServiceMonitor (Prometheus Operator)
🇮🇹 Richiede i CRD del Prometheus Operator (`monitoring.coreos.com/v1`). Su OpenShift
abilita prima user-workload-monitoring. 🇬🇧 Requires the Prometheus Operator CRDs.
```bash
kubectl apply -f deploy/manager/servicemonitor.yaml
```
> 🇮🇹 Non sono in `kubectl apply -k deploy` perché dipendono da CRD/CNI esterni.
> 🇬🇧 They are not in `kubectl apply -k deploy` because they depend on external CRDs/CNI.

---

## 10. Riferimento di configurazione / Configuration reference

🇮🇹 La config si stratifica all'avvio: **default integrati → file YAML (`CONFIG_FILE`) →
variabili d'ambiente**, poi viene validata. Nel Deployment il file è montato dal ConfigMap
`maintenance-orchestrator-config` su `/etc/maintenance-orchestrator/config.yaml` e passato
con `--config`. 🇬🇧 Config is layered at startup: **built-in defaults → YAML file
(`CONFIG_FILE`) → environment variables**, then validated. The Deployment mounts the file
from the ConfigMap at `/etc/maintenance-orchestrator/config.yaml` via `--config`.

| Chiave YAML / key | Env var | Default | Significato / Meaning |
|---|---|---|---|
| `metricsAddr` | `METRICS_ADDR` | `:8080` | bind metriche Prometheus |
| `probeAddr` | `PROBE_ADDR` | `:8081` | bind `/healthz`, `/readyz` |
| `uiEnabled` | `UI_ENABLED` | `false`¹ | abilita la dashboard web |
| `uiAddr` | `UI_ADDR` | `:8082` | bind dashboard |
| `leaderElection` | `LEADER_ELECTION` | `true` | singola istanza attiva |
| `leaderElectionID` | `LEADER_ELECTION_ID` | `maintenance-orchestrator.maintenance.platform.dev` | nome della Lease |
| `reconcileConcurrency` | `RECONCILE_CONCURRENCY` | `2` | reconcile paralleli per controller |
| `evictionPollInterval` | `EVICTION_POLL_INTERVAL` | `5s` | re-check di un nodo in drain |
| `globalRequeueInterval` | `GLOBAL_REQUEUE_INTERVAL` | `30s` | requeue steady-state |
| `defaultDrainTimeout` | `DEFAULT_DRAIN_TIMEOUT` | `15m` | timeout drain per-nodo se non in spec |
| `defaultGlobalTimeout` | `DEFAULT_GLOBAL_TIMEOUT` | `2h` | timeout globale richiesta se non in spec |
| `defaultReplacementTimeout` | `DEFAULT_REPLACEMENT_TIMEOUT` | `20m` | attesa nodo sostitutivo (upgrade) |
| `logLevel` | `LOG_LEVEL` | `info` | `debug\|info\|warn\|error` |
| `logFormat` | `LOG_FORMAT` | `json` | `json\|console` |
| `enableK8sEvents` | `ENABLE_K8S_EVENTS` | `true` | emette Event Kubernetes |
| `defaultPolicyName` | `DEFAULT_POLICY_NAME` | `cluster-default` | policy usata senza `policyRef` |
| `auditExportPath` | `AUDIT_EXPORT_PATH` | _(vuoto)_ | file JSON-lines audit (volume scrivibile) |
| `defaultPoolKeys` | `DEFAULT_POOL_KEYS` (CSV) | label pool note (OCP/EKS/GKE/AKS/Karpenter) | chiavi label trattate come pool |

¹ 🇮🇹 default nel codice `false`; il ConfigMap fornito lo imposta a `true`.
🇬🇧 code default is `false`; the shipped ConfigMap sets it to `true`.

🇮🇹 **Per cambiare la config:** modifica il ConfigMap e riavvia il controller.
🇬🇧 **To change config:** edit the ConfigMap and restart the controller.
```bash
kubectl -n maintenance-orchestrator-system edit configmap maintenance-orchestrator-config
kubectl -n maintenance-orchestrator-system rollout restart deploy/maintenance-orchestrator
```

---

## 11. Cosa concede l'RBAC / What the RBAC grants

🇮🇹 Il ClusterRole `maintenance-orchestrator` è **minimo**: legge/aggiorna i propri CR;
legge/patcha i nodi (cordon/uncordon); legge i pod e crea `pods/eviction` (rispetta i
PDB); `delete` su pod solo per la force eviction policy-gated; legge PDB e workload
(`apps`); crea Event; `delete` su `machines` (OpenShift/Cluster API) per la sostituzione
nodi. La Role di namespace gestisce solo la Lease di leader election.

🇬🇧 The `maintenance-orchestrator` ClusterRole is **minimal**: read/update its own CRs;
read/patch nodes (cordon/uncordon); read pods and create `pods/eviction` (honors PDBs);
`delete` pods only for policy-gated force eviction; read PDBs and workloads; create
Events; `delete` `machines` (OpenShift/Cluster API) for node replacement. The namespaced
Role only manages the leader-election Lease.

```bash
kubectl describe clusterrole maintenance-orchestrator    # ispeziona i permessi esatti
```

---

## 12. OpenShift

🇮🇹 Identico ma con `oc`. Note specifiche:
🇬🇧 Same as above but with `oc`. Specifics:
```bash
oc apply -k deploy
oc apply -f deploy/samples/policy-cluster-default.yaml
```
- **SCC**: il pod gira non-root (uid 65532), senza capability aggiunte, con
  `seccompProfile: RuntimeDefault` e root FS read-only → compatibile con `restricted-v2`
  **senza** SCC custom.
- **Monitoring**: per lo scrape via user-workload-monitoring abilita quel monitoring e
  applica `deploy/manager/servicemonitor.yaml`.
- **MCO**: i nodi in aggiornamento Machine Config vengono marcati `Skipped`.
- **Upgrade per sostituzione**: usa `machine.openshift.io` come Machine API e la policy
  con `allowNodeReplacement: true` (vedi `deploy/samples/mreq-pool-upgrade.yaml`).

---

## 13. Upgrade dell'operatore / Upgrading the operator

🇮🇹 **Solo nuova immagine** (nessun cambio di CRD): aggiorna il tag.
🇬🇧 **Image-only** (no CRD change): bump the tag.
```bash
kubectl -n maintenance-orchestrator-system set image \
  deployment/maintenance-orchestrator manager=registry.example.com/maintenance-orchestrator:vNEXT
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator
```
🇮🇹 **Con nuovi campi CRD**: applica prima i CRD aggiornati, poi l'immagine. I CRD sono
additivi/retro-compatibili; i CR esistenti restano validi.
🇬🇧 **With new CRD fields**: apply the updated CRDs first, then the image. CRD changes are
additive/backward-compatible; existing CRs stay valid.
```bash
kubectl apply -f deploy/crd
kubectl apply -k deploy
```

---

## 14. Disinstallazione / Uninstall

> ⚠️ 🇮🇹 Eliminare i CRD **cancella tutti** i `MaintenanceRequest`/`MaintenancePolicy`.
> Concludi o annulla le richieste attive prima. 🇬🇧 Deleting the CRDs **removes all**
> `MaintenanceRequest`/`MaintenancePolicy`. Finish or cancel active requests first.

```bash
# 1) (consigliato) chiudi le richieste attive / close active requests
kubectl get mreq

# 2) rimuovi manager + RBAC (kustomize o make)
kubectl delete -k deploy --ignore-not-found     # rimuove crd, rbac, manager
#   ...oppure / ...or:
# make undeploy && make uninstall

# 3) rimuovi i CR rimasti e il namespace
kubectl delete mpol --all
kubectl delete namespace maintenance-orchestrator-system --ignore-not-found

# 4) rimuovi i CRD (se non già rimossi al punto 2)
kubectl delete -f deploy/crd --ignore-not-found
```

---

## 15. Troubleshooting dell'installazione / Install troubleshooting

| Sintomo / Symptom | Causa & rimedio / Cause & fix |
|---|---|
| `kubectl apply` su CRD/ClusterRole → `forbidden` | 🇮🇹 Non sei cluster-admin. 🇬🇧 You are not cluster-admin (section 1.1). |
| Pod `ImagePullBackOff` / `ErrImageNeverPull` | 🇮🇹 Immagine non nel cluster. 🇬🇧 Image not present. Docker Desktop: rebuild. kind: `kind load docker-image`. Reale/Real: push + `set image` (5.4) + eventuale `imagePullSecret` (3.D). |
| Pod `CrashLoopBackOff`, log `invalid configuration` | 🇮🇹 Valore di config non valido (es. `logFormat`, durate ≤ 0, `uiAddr` vuoto con `uiEnabled`). 🇬🇧 Invalid config value — fix the ConfigMap, restart. |
| Pod `Running` ma `0/1` Ready a lungo | 🇮🇹 `readyz` non passa: controlla i log; spesso RBAC mancante o cache non sincronizzata. 🇬🇧 RBAC missing or cache not synced — check logs. |
| `mpol cluster-default` non trovata / requests restano `Pending` | 🇮🇹 Hai saltato la policy. 🇬🇧 You skipped the policy: `kubectl apply -f deploy/samples/policy-cluster-default.yaml`. |
| `error: unable to recognize ... no matches for kind "MaintenanceRequest"` | 🇮🇹 CRD non ancora `Established`. 🇬🇧 CRDs not Established yet — run the `kubectl wait` from 5.2. |
| `kubectl apply -k deploy` errore kustomize | 🇮🇹 kubectl troppo vecchio. 🇬🇧 Update kubectl, or `kubectl kustomize deploy \| kubectl apply -f -`. |
| Dashboard non raggiungibile / not reachable | 🇮🇹 `uiEnabled` false, oppure manca il port-forward, oppure NetworkPolicy blocca 8082. 🇬🇧 `uiEnabled` false, missing port-forward, or NetworkPolicy blocking 8082. |
| Metriche vuote / empty metrics | 🇮🇹 Verifica il port-forward su 8080 e il nome service `maintenance-orchestrator-metrics`. 🇬🇧 Check the 8080 port-forward and the metrics service name. |
| Più repliche ma una sola lavora | 🇮🇹 Atteso: la leader election lascia attivo un solo reconciler (la dashboard gira su tutte). 🇬🇧 Expected: leader election keeps one active reconciler (the dashboard runs on all). |

---

## 16. Esecuzione locale (sviluppo) / Run locally (development)

🇮🇹 Per sviluppo puoi eseguire il controller **fuori dal cluster**, contro il kubeconfig
corrente (i CRD devono essere installati: `make install`).
🇬🇧 For development you can run the controller **out-of-cluster** against the current
kubeconfig (CRDs must be installed: `make install`).
```bash
make install                      # applica i CRD / apply the CRDs
kubectl apply -f deploy/samples/policy-cluster-default.yaml
make run                          # go run ./cmd/manager --config hack/config.local.yaml
```
🇮🇹 La dashboard è già abilitata in `hack/config.local.yaml` → `http://localhost:8082`.
🇬🇧 The dashboard is already enabled in `hack/config.local.yaml` → `http://localhost:8082`.
