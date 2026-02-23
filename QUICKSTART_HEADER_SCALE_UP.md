# Quick Start: headerForScaleUp Feature

## 🚀 5-Minute Setup

### 1. Enable Feature (Helm)

```yaml
# values.yaml
elastiResolver:
  proxy:
    env:
      headerForScaleUp: "X-Scale-Up-Services"
```

```bash
helm upgrade elasti ./charts/elasti -f values.yaml -n elasti-system
```

### 2. Configure Ingress

**NGINX:**
```yaml
annotations:
  nginx.ingress.kubernetes.io/configuration-snippet: |
    proxy_set_header X-Scale-Up-Services "service1,service2.namespace2";
```

**Istio:**
```yaml
headers:
  request:
    set:
      X-Scale-Up-Services: "service1,service2.namespace2"
```

### 3. Test

```bash
curl http://your-service.com/ \
  -H "X-Scale-Up-Services: service1,service2.namespace2"
```

## 📝 Header Format

```
X-Scale-Up-Services: service1,service2.namespace2,service3
```

- `service1` → scales in same namespace as target
- `service2.namespace2` → scales in namespace2
- `service3` → scales in same namespace as target

## ✅ Validation Rules

- ✅ Lowercase letters, numbers, hyphens
- ✅ Start/end with letter or number
- ✅ Max 253 characters per name
- ❌ No underscores, uppercase, or special chars

## 🔍 Verify

```bash
# Check resolver logs
kubectl logs -n elasti-system deployment/elasti-resolver | grep "Scaling additional"

# Watch services scale
watch kubectl get deployments -A
```

## 📖 Full Documentation

- **User Guide**: `docs/src/feature-header-scale-up.md`
- **Example**: `examples/multi-service-scale-up/`
- **Technical**: `HEADER_SCALE_UP_FEATURE.md`
