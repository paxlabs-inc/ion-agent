# Ion on Kubernetes

Plain Kubernetes manifests with a Kustomize base.

## Topology

Ion serves plain HTTP on the Pod's loopback interface only. A Caddy sidecar
shares the Pod network namespace, reaches Ion at `127.0.0.1:4174`, and exposes
port `8080`. The `Service` targets `8080`; the `Ingress` terminates TLS.

```text
Ingress (TLS) ─ Service :80 ─ Pod[ proxy :8080 ─ 127.0.0.1:4174 ─ ion ]
                                                                    └─ PVC /data
```

Ion is a single persistent identity backed by a `ReadWriteOnce` volume, so the
Deployment runs exactly one replica with the `Recreate` strategy.

## Apply

```bash
kubectl create namespace ion
kubectl -n ion create secret generic ion-operator-auth \
  --from-literal=username=operator \
  --from-literal=password='replace-with-a-generated-password'
kubectl -n ion create secret generic ion-office-secrets \
  --from-literal=jwt-secret="$(openssl rand -hex 32)"
kubectl apply -k deploy/kubernetes
```

Edit before applying:

- `ingress.yaml` — set your host, TLS secret, ingress class, and issuer.
- `configmap.yaml` — set `ION_WEB_ORIGIN` to that exact HTTPS host.
- `pvc.yaml` — set `storageClassName` and size.
- `kustomization.yaml` — pin the image tag.
- `office-deployment.yaml` — pin the ONLYOFFICE image digest if your release
  process requires digest-level reproducibility.

The `ion-operator-auth` Secret must contain `username` and exactly one of
`password` or `passwordHash`. Do not commit the populated Secret manifest.
The `ion-office-secrets` Secret must contain the same `jwt-secret` presented to
Ion and ONLYOFFICE.

## Initialize the vault

The manifests do not auto-initialize (`ION_AUTO_INIT: "0"`). Key management is an
operator responsibility. Initialize once against your chosen key source, for
example:

```bash
kubectl -n ion exec deploy/ion -c ion -- ion init --data-dir /data
```

Review [SECURITY.md](../../SECURITY.md) for the key hierarchy and boundaries
before choosing a production key source. The development file KEK is not a
production mechanism.

## Notes

- Pods run under the `restricted` Pod Security Standard: non-root, no privilege
  escalation, read-only root filesystem, all capabilities dropped.
- For parameterized installs, prefer the [Helm chart](../helm/ion/).
