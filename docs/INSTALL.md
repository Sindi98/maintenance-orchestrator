# Guida d'installazione — Maintenance Orchestrator

> 🇮🇹 Italiano · 🇬🇧 English version: [`INSTALL.en.md`](INSTALL.en.md)

Questa guida documenta **ogni passo** dell'installazione, senza dare nulla per scontato:
prerequisiti esatti, build dell'immagine, distribuzione dell'immagine al cluster,
installazione (3 metodi), verifica, accesso alla dashboard, riferimento di configurazione
completo, OpenShift, upgrade, disinstallazione e troubleshooting.

Per la **simulazione su Docker Desktop/kind** vedi [`DEMO.md`](DEMO.md); per
l'**architettura** [`DESIGN.md`](DESIGN.md).

---

## 0. Cosa viene installato

Il prodotto è **un solo controller** (un Deployment) più i suoi CRD e RBAC. Non usa
database esterni: lo stato vive negli oggetti Kubernetes (`.status` dei CR).

| Oggetto | Nome | Scope | Note |
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

**Porte del container:** `8080` metriche · `8081` health (`/healthz`, `/readyz`) ·
`8082` dashboard web (se `uiEnabled`).

> ⚠️ CRD e ClusterRole/Binding sono **cluster-scoped**: installarli richiede privilegi di
> **cluster-admin** (o equivalente).

---

## 1. Prerequisiti

### 1.1 Sempre necessari
- **Un cluster Kubernetes** ≥ 1.27 (vanilla, EKS/GKE/AKS, OpenShift ≥ 4.12, kind, Docker
  Desktop). Il controller usa solo API stabili (`core/v1`, `apps/v1`, `policy/v1`).
- **`kubectl`** ≥ 1.27 configurato sul cluster target (`kubectl config current-context`).
- **Privilegi cluster-admin** (per CRD + ClusterRole). Verifica:
  ```bash
  kubectl auth can-i create customresourcedefinitions
  kubectl auth can-i create clusterroles
  # entrambi devono stampare: yes
  ```

### 1.2 Necessari solo per costruire l'immagine
- **Docker** (o Podman) per `docker build`.
- *(alternativa)* **Go ≥ 1.22** + **make** per il binario (`make build`) o per i target
  del Makefile. Modulo: `github.com/Sindi98/maintenance-orchestrator`.

> Se hai già un'immagine pubblicata, **salta la sezione 2** e vai alla 3.

### 1.3 Verifica rapida
```bash
kubectl version
kubectl config current-context
kubectl get nodes -o wide
docker version            # solo se costruisci l'immagine
go version                # idem
```

---

## 2. Ottieni il sorgente e costruisci l'immagine

```bash
git clone https://github.com/Sindi98/maintenance-orchestrator.git
cd maintenance-orchestrator
```

Il `Dockerfile` produce un binario statico (`CGO_ENABLED=0`, `-trimpath`, strip) su
`gcr.io/distroless/static:nonroot` (utente **uid 65532**, FS minimale) — compatibile con
la SCC OpenShift `restricted-v2` senza personalizzazioni. I template/CSS/JS della
dashboard sono **embeddati** nel binario (`go:embed`): nessun passo Node/build.

```bash
# Scegli un tag. Per Docker Desktop va bene anche il default :latest.
export IMG=maintenance-orchestrator:latest

# Makefile (esegue 'docker build -t $IMG .')
make docker-build IMG=$IMG

# ...oppure docker diretto
docker build -t "$IMG" .
```

**Multi-arch:** il `Dockerfile` rispetta `TARGETOS/TARGETARCH`. Per un cluster di
architettura diversa usa buildx:
```bash
docker buildx build --platform linux/amd64,linux/arm64 -t registry.example.com/maintenance-orchestrator:v0.1.0 --push .
```

---

## 3. Rendi l'immagine disponibile al cluster

Il Deployment usa `imagePullPolicy: IfNotPresent` e l'immagine placeholder
`maintenance-orchestrator:latest`. Il modo di renderla disponibile dipende dal cluster.

### 3.A Docker Desktop (Kubernetes integrato)
Il cluster condivide il daemon Docker: l'immagine appena costruita è **già visibile**,
nessun push. Usa il tag `maintenance-orchestrator:latest` così com'è.

### 3.B kind
```bash
kind load docker-image maintenance-orchestrator:latest --name <cluster>
```

### 3.C Cluster reale (registry)
Tagga e pusha su un registry raggiungibile dal cluster; userai quel riferimento nella
sezione 5.4.
```bash
docker tag maintenance-orchestrator:latest registry.example.com/maintenance-orchestrator:v0.1.0
docker push registry.example.com/maintenance-orchestrator:v0.1.0
```

### 3.D Registry privato (imagePullSecret)
```bash
kubectl -n maintenance-orchestrator-system create secret docker-registry regcred \
  --docker-server=registry.example.com --docker-username=USER --docker-password=PASS
# poi aggiungi a deploy/manager/deployment.yaml, sotto spec.template.spec:
#   imagePullSecrets:
#     - name: regcred
```

> **Air-gapped:** porta l'immagine nel registry interno con `docker save | docker load`
> o `skopeo copy`, poi usa quel riferimento.

---

## 4. (Solo cluster reale) Crea il namespace e i secret

I metodi step-by-step (5.2) e `make deploy` (5.3) creano il namespace da soli. Se ti
serve prima (es. per il `regcred` di sopra), crealo a mano:
```bash
kubectl apply -f deploy/manager/namespace.yaml
```

---

## 5. Installazione — scegli UN metodo

### 5.1 Metodo A — Kustomize (consigliato)
Applica in un colpo **CRD + RBAC + manager** (namespace, configmap, deployment, service
metriche, service UI). La policy di default e i sample NON sono inclusi (sono CR che
richiedono i CRD già `Established`).
```bash
kubectl apply -k deploy
kubectl apply -f deploy/samples/policy-cluster-default.yaml
```

### 5.2 Metodo B — Passo-passo (ordine esplicito)
Utile per capire/auditare ogni risorsa. **L'ordine conta**: i CRD devono essere
`Established` prima di creare CR (la policy).
```bash
# 1) Namespace
kubectl apply -f deploy/manager/namespace.yaml

# 2) CRD, poi ATTENDI che siano Established
kubectl apply -f deploy/crd
kubectl wait --for=condition=Established --timeout=60s \
  crd/maintenancerequests.maintenance.platform.dev \
  crd/maintenancepolicies.maintenance.platform.dev

# 3) RBAC (ServiceAccount, ClusterRole/Binding, leader-election Role/Binding)
kubectl apply -f deploy/rbac

# 4) Policy di default (nome atteso dal controller via defaultPolicyName)
kubectl apply -f deploy/samples/policy-cluster-default.yaml

# 5) Config + controller + service
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

### 5.4 Imposta l'immagine pushata
**Solo cluster reali** (sezione 3.C): il manifest usa `:latest` come placeholder.
Sostituiscila col tuo riferimento:
```bash
kubectl -n maintenance-orchestrator-system set image \
  deployment/maintenance-orchestrator \
  manager=registry.example.com/maintenance-orchestrator:v0.1.0
```

---

## 6. Verifica l'installazione

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

# 6) Health & metriche (port-forward su 8081/8080)
kubectl -n maintenance-orchestrator-system port-forward deploy/maintenance-orchestrator 8081:8081 8080:8080 &
curl -fsS localhost:8081/healthz && echo " <- healthz ok"
curl -fsS localhost:8081/readyz  && echo " <- readyz ok"
curl -fsS localhost:8080/metrics | grep -E 'active_maintenances|maintenance_requests_total' | head
kill %1 2>/dev/null
```

Tutto verde = installato. Se il pod non è `Running`, salta alla sezione 15.

---

## 7. Accedi alla dashboard web

La dashboard è abilitata di default nel ConfigMap (`uiEnabled: true`, porta `:8082`).
**Non ha autenticazione**, perciò è esposta solo come `ClusterIP`.
```bash
kubectl -n maintenance-orchestrator-system port-forward svc/maintenance-orchestrator-ui 8082:8082
# apri  ->  http://localhost:8082
```
In produzione mettila dietro un **ingress/route con autenticazione**, oppure disattivala
con `uiEnabled: false` (sezione 10).

---

## 8. Smoke test end-to-end (DryRun, non distruttivo)

Conferma che il controller riconcilia davvero. Il DryRun non modifica nulla.
```bash
# Nodo fittizio solo per il test (oggetto API, nessuna VM)
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

# Atteso: phase Completed con un plan in .status
kubectl get mreq smoke-test -o jsonpath='{.status.phase}{"  "}{.status.plan.totalNodes}{" node(s)\n"}'

# Pulizia
kubectl delete mreq smoke-test --ignore-not-found
kubectl delete node smoke-node-1 --ignore-not-found
```

---

## 9. Componenti opzionali

### 9.1 NetworkPolicy
Limita ingress (8080/8081/8082) ed egress (DNS + API server). Richiede un CNI che applichi
le NetworkPolicy (Calico, Cilium, OVN-Kubernetes).
```bash
kubectl apply -f deploy/manager/networkpolicy.yaml
```

### 9.2 ServiceMonitor (Prometheus Operator)
Richiede i CRD del Prometheus Operator (`monitoring.coreos.com/v1`). Su OpenShift abilita
prima user-workload-monitoring.
```bash
kubectl apply -f deploy/manager/servicemonitor.yaml
```
> Non sono in `kubectl apply -k deploy` perché dipendono da CRD/CNI esterni.

---

## 10. Riferimento di configurazione

La config si stratifica all'avvio: **default integrati → file YAML (`CONFIG_FILE`) →
variabili d'ambiente**, poi viene validata. Nel Deployment il file è montato dal ConfigMap
`maintenance-orchestrator-config` su `/etc/maintenance-orchestrator/config.yaml` e passato
con `--config`.

| Chiave YAML | Env var | Default | Significato |
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

¹ default nel codice `false`; il ConfigMap fornito lo imposta a `true`.

**Per cambiare la config:** modifica il ConfigMap e riavvia il controller.
```bash
kubectl -n maintenance-orchestrator-system edit configmap maintenance-orchestrator-config
kubectl -n maintenance-orchestrator-system rollout restart deploy/maintenance-orchestrator
```

---

## 11. Cosa concede l'RBAC

Il ClusterRole `maintenance-orchestrator` è **minimo**: legge/aggiorna i propri CR;
legge/patcha i nodi (cordon/uncordon); legge i pod e crea `pods/eviction` (rispetta i
PDB); `delete` su pod solo per la force eviction policy-gated; legge PDB e workload
(`apps`); crea Event; `delete` su `machines` (OpenShift/Cluster API) per la sostituzione
nodi. La Role di namespace gestisce solo la Lease di leader election.
```bash
kubectl describe clusterrole maintenance-orchestrator    # ispeziona i permessi esatti
```

---

## 12. OpenShift

Identico ma con `oc`:
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

## 13. Upgrade dell'operatore

**Solo nuova immagine** (nessun cambio di CRD): aggiorna il tag.
```bash
kubectl -n maintenance-orchestrator-system set image \
  deployment/maintenance-orchestrator manager=registry.example.com/maintenance-orchestrator:vNEXT
kubectl -n maintenance-orchestrator-system rollout status deploy/maintenance-orchestrator
```
**Con nuovi campi CRD**: applica prima i CRD aggiornati, poi l'immagine. I CRD sono
additivi/retro-compatibili; i CR esistenti restano validi.
```bash
kubectl apply -f deploy/crd
kubectl apply -k deploy
```

---

## 14. Disinstallazione

> ⚠️ Eliminare i CRD **cancella tutti** i `MaintenanceRequest`/`MaintenancePolicy`.
> Concludi o annulla le richieste attive prima.

```bash
# 1) (consigliato) controlla le richieste attive
kubectl get mreq

# 2) rimuovi manager + RBAC (kustomize o make)
kubectl delete -k deploy --ignore-not-found     # rimuove crd, rbac, manager
#   ...oppure: make undeploy && make uninstall

# 3) rimuovi i CR rimasti e il namespace
kubectl delete mpol --all
kubectl delete namespace maintenance-orchestrator-system --ignore-not-found

# 4) rimuovi i CRD (se non già rimossi al punto 2)
kubectl delete -f deploy/crd --ignore-not-found
```

---

## 15. Troubleshooting dell'installazione

| Sintomo | Causa & rimedio |
|---|---|
| `kubectl apply` su CRD/ClusterRole → `forbidden` | Non sei cluster-admin (sezione 1.1). |
| Pod `ImagePullBackOff` / `ErrImageNeverPull` | Immagine non nel cluster. Docker Desktop: rebuild. kind: `kind load docker-image`. Reale: push + `set image` (5.4) + eventuale `imagePullSecret` (3.D). |
| Pod `CrashLoopBackOff`, log `invalid configuration` | Valore di config non valido (es. `logFormat`, durate ≤ 0, `uiAddr` vuoto con `uiEnabled`). Correggi il ConfigMap, riavvia. |
| Pod `Running` ma `0/1` Ready a lungo | `readyz` non passa: controlla i log; spesso RBAC mancante o cache non sincronizzata. |
| `mpol cluster-default` non trovata / richieste restano `Pending` | Hai saltato la policy: `kubectl apply -f deploy/samples/policy-cluster-default.yaml`. |
| `no matches for kind "MaintenanceRequest"` | CRD non ancora `Established` — esegui il `kubectl wait` di 5.2. |
| `kubectl apply -k deploy` errore kustomize | kubectl troppo vecchio, oppure `kubectl kustomize deploy \| kubectl apply -f -`. |
| Dashboard non raggiungibile | `uiEnabled` false, oppure manca il port-forward, oppure NetworkPolicy blocca 8082. |
| Metriche vuote | Verifica il port-forward su 8080 e il service `maintenance-orchestrator-metrics`. |
| Più repliche ma una sola lavora | Atteso: la leader election lascia attivo un solo reconciler (la dashboard gira su tutte). |

---

## 16. Esecuzione locale (sviluppo)

Per sviluppo puoi eseguire il controller **fuori dal cluster**, contro il kubeconfig
corrente (i CRD devono essere installati: `make install`).
```bash
make install                      # applica i CRD
kubectl apply -f deploy/samples/policy-cluster-default.yaml
make run                          # go run ./cmd/manager --config hack/config.local.yaml
```
La dashboard è già abilitata in `hack/config.local.yaml` → `http://localhost:8082`.
