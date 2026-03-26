# K8s Pipeline: OrbStack + Native Dolt on macOS

End-to-end guide for running Gas City agents as K8s pods on a Mac Mini,
with Dolt running natively on the host for minimal VM overhead.

## Architecture

Keep heavy services (Dolt, controller, mail) native on macOS. Only agent
workloads run inside K8s. OrbStack dynamically shares memory — no fixed
VM allocation.

```
Mac Mini (16GB)
├── macOS + system:          ~3-4GB
├── Dolt sql-server (native): ~200MB, port 3307
├── gc controller (native):   ~100MB
├── OrbStack:                 ~200MB base, grows on demand
│   └── k3s
│       ├── agent pod: mayor
│       ├── agent pod: coder-1
│       └── agent pod: coder-2
└── Free:                     ~11-12GB (dynamic)
```

Agent pods connect to the host's Dolt via `host.internal` (OrbStack's
host gateway). No Dolt StatefulSet needed inside the cluster.

## Prerequisites

- macOS with Apple Silicon (arm64)
- [Gas City](https://github.com/gastownhall/gascity) built and working locally
- [Dolt](https://docs.dolthub.com/introduction/installation) installed
- Claude credentials at `~/.claude`

## Step-by-Step Pipeline

### Phase 1: Infrastructure (OrbStack + K8s)

1. **Install OrbStack**
   ```bash
   brew install orbstack
   ```
   Open OrbStack, enable Kubernetes in Settings → Kubernetes.
   OrbStack takes over the Docker socket and provides kubectl automatically.

2. **Verify**
   ```bash
   kubectl get nodes
   kubectl cluster-info
   docker info | grep -i orbstack
   ```

### Phase 2: Native Services + K8s Manifests

3. **Start Dolt natively on the Mac**
   ```bash
   mkdir -p ~/dolt-data/gascity && cd ~/dolt-data/gascity
   dolt init  # only first time
   dolt sql-server --host 0.0.0.0 --port 3307 --max-connections 500 &
   ```
   Verify: `mysql -h 127.0.0.1 -P 3307 -u root -e "SELECT 1"`

4. **Apply K8s manifests** (namespace + RBAC only — no Dolt)
   ```bash
   kubectl apply -f contrib/k8s/namespace.yaml
   kubectl apply -f contrib/k8s/rbac.yaml
   ```

5. **Create Claude credentials secret**
   ```bash
   make k8s-secret CLAUDE_CONFIG_SRC=~/.claude
   ```

### Phase 3: Build Images

6. **Cross-compile binaries for Linux arm64**

   The Docker images run Linux inside OrbStack's VM. You need Linux
   binaries, not macOS:
   ```bash
   GOOS=linux GOARCH=arm64 go build -o bin/gc-linux ./cmd/gc
   GOOS=linux GOARCH=arm64 go build -o bin/bd-linux ./cmd/bd
   ```

   Copy them to the build context locations expected by the Dockerfiles
   (check `contrib/k8s/Dockerfile.agent` for exact paths).

   If `br` (Rust binary) isn't available for Linux arm64, create a stub:
   ```bash
   echo '#!/bin/sh' > bin/br-linux && chmod +x bin/br-linux
   ```
   The default beads provider is `bd`, so `br` won't be called at
   runtime, but `Dockerfile.agent` COPY requires it to exist.

7. **Build Docker images**
   ```bash
   make docker-base docker-agent
   ```
   OrbStack shares the Docker daemon with k3s, so images are immediately
   available to the cluster — no registry push needed.

### Phase 4: City Configuration

8. **Create a fresh city directory**
   ```bash
   gc init --file contrib/k8s/example-city.toml ~/cities/k8s-test
   ```
   Use a fresh path — `gc init` won't overwrite an existing city.

9. **Edit city.toml** with native Dolt + K8s agents:
   ```toml
   [workspace]
   name = "k8s-test"
   provider = "claude"
   session_template = "{{.City}}-{{.Agent}}"

   [session]
   provider = "k8s"

   [session.k8s]
   namespace = "gc"
   image = "gc-agent:latest"
   cpu_request = "500m"
   mem_request = "1Gi"

   [dolt]
   host = "host.internal"    # OrbStack host gateway
   port = 3307

   [mail]
   provider = "exec:gc-mail-mcp-agent-mail"

   [[agent]]
   name = "mayor"
   prompt_template = "prompts/mayor.md.tmpl"
   install_hooks = ["claude"]
   process_names = ["claude"]
   ready_prompt_prefix = "claude"
   nudge = "Check mail and hook status, then act accordingly."

   [[agent]]
   name = "coder"
   prompt_template = "prompts/coder.md.tmpl"
   install_hooks = ["claude"]
   process_names = ["claude"]
   ready_prompt_prefix = "claude"
   nudge = "Check your hook for work assignments."
   ```

   The key line is `host = "host.internal"` — this tells agents inside
   pods to reach the Mac's native Dolt. OrbStack resolves this to the
   host IP automatically.

   For Colima or Docker Desktop, use `host.docker.internal` instead.

### Phase 5: Start & Verify

10. **Start the city**
    ```bash
    export GC_K8S_IMAGE=gc-agent:latest
    gc start --foreground ~/cities/k8s-test
    ```
    The controller runs natively on macOS. It creates agent pods in K8s
    via the native K8s provider (client-go).

11. **Verify agents are running**
    ```bash
    kubectl -n gc get pods
    gc status
    ```

12. **Test agent interaction**
    ```bash
    gc attach mayor          # Attach to agent tmux
    gc peek mayor            # View agent output
    gc nudge mayor "hello"   # Send message
    ```

13. **Verify cross-agent bead sharing**

    Create a bead from mayor, then check if coder can see it. Both
    agents connect to the same native Dolt instance on the host.

## Expanding to Multiple Machines

Once single-node works, the simplest multi-machine step:

**Run agents on mini2 pointing at mini3's Dolt over the network.**

On mini2's city.toml:
```toml
[dolt]
host = "mini3.tailscale.net"   # or LAN IP
port = 3307
```

This proves cross-machine beads without any cluster federation. Other
options for later:

- **k3s cluster join**: mini2's OrbStack joins mini3's k3s cluster
- **Hybrid mode**: Mayor local on mini3 + workers as K8s pods on both
- **Independent cities**: Each Mini runs its own city, sharing Dolt

## Key Files

| File | Purpose |
|------|---------|
| `contrib/k8s/Dockerfile.base` | Base image (Ubuntu + tmux + claude + dolt client) |
| `contrib/k8s/Dockerfile.agent` | Agent image (gc + bd binaries) |
| `contrib/k8s/namespace.yaml` | K8s namespace |
| `contrib/k8s/rbac.yaml` | Agent RBAC |
| `contrib/k8s/example-city.toml` | Reference config |
| `internal/runtime/k8s/provider.go` | Native K8s provider |
| `internal/runtime/k8s/pod.go` | Pod manifest generation |
| `cmd/gc/bd_env.go` | Dolt config wiring (issue 011) |

## Known Constraints

- OrbStack k3s is single-node — fine for proving the pipeline
- Agent pods need ~500m CPU + 1Gi RAM each (default requests)
- With dynamic memory, ~6-8 agent pods fit comfortably on 16GB
- `host.internal` is OrbStack-specific; use `host.docker.internal` for Colima/Docker Desktop
- `br` binary may need a stub if not cross-compiled for Linux arm64

## Troubleshooting

**Agents can't reach Dolt:**
Check that Dolt is listening on `0.0.0.0` (not `127.0.0.1`) and that
`host.internal` resolves from inside a pod:
```bash
kubectl -n gc exec <pod> -- nslookup host.internal
kubectl -n gc exec <pod> -- mysql -h host.internal -P 3307 -u root -e "SELECT 1"
```

**Controller can't create pods:**
Verify RBAC is applied and the kubeconfig context is correct:
```bash
kubectl auth can-i create pods -n gc
```

**Images not found by k3s:**
OrbStack must be the active Docker context. Check with:
```bash
docker context ls
```

## Rollback to Colima

If OrbStack doesn't work out:
```bash
colima start --kubernetes --cpu 2 --memory 3 --disk 60
```
All manifests, images, and city config work the same — just change
`host.internal` → `host.docker.internal` in the Dolt config.
