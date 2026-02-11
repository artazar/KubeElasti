---
title: "Multi-Service Scale-Up with headerForScaleUp"
description: "Scale multiple dependent services simultaneously from a single HTTP request using the headerForScaleUp feature"
keywords:
  - multi-service scaling
  - dependent services
  - headerForScaleUp
  - batch scale-up
  - service dependencies
---

# Multi-Service Scale-Up Feature

## Overview

The `headerForScaleUp` feature enables KubeElasti to scale up multiple services simultaneously from a single HTTP request. This is particularly useful for microservice architectures where multiple dependent services need to be available before processing a request.

## Use Cases

- **Service Dependencies**: When your primary service depends on multiple backend services (e.g., cache, database proxy, message queue)
- **Microservice Chains**: Complex request flows that involve multiple microservices
- **Batch Warm-Up**: Reducing cold start latency by pre-warming dependent services
- **Cost Optimization**: Keep all services scaled to zero, wake them all up together when traffic arrives

## How It Works

1. An HTTP request arrives at the KubeElasti resolver
2. The resolver extracts the main target service from the `headerForHost` header (e.g., `Host` or `X-Envoy-Decorator-Operation`)
3. If configured, the resolver also reads the `headerForScaleUp` header containing additional services
4. The resolver sends scale-up requests to the operator for:
   - The main target service (as usual)
   - All additional services listed in `headerForScaleUp` header
5. Each service scales up independently and concurrently
6. The main request is queued and proxied once the target service is ready

## Configuration

### Resolver Configuration

Add the `headerForScaleUp` configuration to your resolver deployment:

**Helm values.yaml:**
```yaml
elastiResolver:
  proxy:
    env:
      headerForHost: X-Envoy-Decorator-Operation
      headerForScaleUp: "X-Scale-Up-Services"  # Custom header name
```

**Environment Variable:**
```bash
HEADER_FOR_SCALE_UP=X-Scale-Up-Services
```

### Ingress/Gateway Configuration

Configure your ingress controller or service mesh to set the custom header on requests:

#### Istio VirtualService Example

```yaml
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: my-service
spec:
  hosts:
    - my-service.example.com
  http:
    - match:
        - uri:
            prefix: /
      headers:
        request:
          set:
            X-Scale-Up-Services: "cache-service,db-proxy.database,queue-service"
      route:
        - destination:
            host: my-service
            port:
              number: 8080
```

#### NGINX Ingress Example

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-service
  annotations:
    nginx.ingress.kubernetes.io/configuration-snippet: |
      proxy_set_header X-Scale-Up-Services "cache-service,db-proxy.database,queue-service";
spec:
  rules:
    - host: my-service.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-service
                port:
                  number: 8080
```

#### Envoy Gateway Example

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: scale-up-policy
spec:
  targetRef:
    group: gateway.networking.k8s.io
    kind: HTTPRoute
    name: my-service-route
  requestHeaderModifier:
    set:
      - name: X-Scale-Up-Services
        value: "cache-service,db-proxy.database,queue-service"
```

## Header Format

The `headerForScaleUp` header should contain a **comma-separated list** of service names:

### Format Options

1. **Service name only** (uses the same namespace as the target service):
   ```
   X-Scale-Up-Services: service1,service2,service3
   ```

2. **Service with explicit namespace** (format: `service.namespace`):
   ```
   X-Scale-Up-Services: cache-service,db-proxy.database,queue.messaging
   ```

3. **Mixed format**:
   ```
   X-Scale-Up-Services: cache-service,db-proxy.database,queue,auth.security
   ```
   - `cache-service` → scales in the same namespace as target
   - `db-proxy.database` → scales in the `database` namespace
   - `queue` → scales in the same namespace as target
   - `auth.security` → scales in the `security` namespace

### Naming Constraints

Service and namespace names must follow **Kubernetes DNS-1123 label** standard:
- Lowercase alphanumeric characters or hyphens (`-`)
- Must start and end with an alphanumeric character
- Maximum 253 characters
- Examples: `my-service`, `cache-1`, `db-proxy`

**Invalid names** (will be skipped with a warning):
- `My-Service` (uppercase)
- `my_service` (underscore)
- `-service` (starts with hyphen)
- `service-` (ends with hyphen)

## Header Size Limitations

### HTTP Header Size Limits

Most HTTP implementations have header size limits:
- **Typical limit**: 8 KB per header
- **NGINX default**: 4-8 KB
- **Apache default**: 8 KB
- **Envoy default**: 60 KB

### Recommendations

1. **Keep lists short**: Aim for under 50 services per header
2. **Use short service names**: Avoid excessively long names
3. **Monitor header size**: Watch for warnings in resolver logs
4. **Consider alternatives**: For very large dependency graphs, use service-to-service calls instead

### Size Calculation

Approximate header size: `len(service_names) + commas + whitespace`

**Example:**
```
cache,db,queue,auth  # ~19 bytes
service1.ns1,service2.ns2,service3.ns3  # ~42 bytes
```

## Behavior and Error Handling

### Normal Operation

1. Resolver parses the header and validates each service name
2. Valid services trigger concurrent scale-up RPC calls to the operator
3. Invalid service names are skipped (logged as warnings)
4. Main request processing continues regardless of additional service scale-up status
5. Each service scales independently according to its ElastiService CRD configuration

### Error Scenarios

| Scenario | Behavior |
|----------|----------|
| Header not configured | Feature is disabled (backward compatible) |
| Header not present in request | No additional services scaled (normal operation) |
| Empty header value | No additional services scaled |
| Header size > 8 KB | Warning logged, but processing continues |
| Invalid service name | Service skipped, warning logged, others still processed |
| Service not found (no ElastiService CRD) | Operator logs error, main request unaffected |
| Operator RPC failure | Existing retry logic handles it, main request unaffected |
| One service fails to scale | Others continue, main request unaffected |

### Graceful Degradation

- The main request is **never blocked** by additional service scale-ups
- All scale-up calls run in separate goroutines (concurrent)
- If an additional service fails, it doesn't affect the main service or request
- Missing or invalid services are skipped silently (with logging)

## Example: Complete Setup

### Scenario

- Main service: `api-gateway`
- Dependencies: `user-cache`, `session-store.redis`, `auth-service.security`
- All services should scale from 0 when traffic arrives

### Step 1: Configure Resolver

```yaml
# values.yaml
elastiResolver:
  proxy:
    env:
      headerForHost: Host
      headerForScaleUp: X-Scale-Up-Services
```

### Step 2: Create ElastiService CRDs

```yaml
apiVersion: elasti.truefoundry.com/v1alpha1
kind: ElastiService
metadata:
  name: api-gateway
  namespace: default
spec:
  service: api-gateway
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: api-gateway
  minTargetReplicas: 1
  triggers:
    - type: prometheus
      metadata:
        query: 'sum(rate(http_requests_total{service="api-gateway"}[2m]))'
        threshold: "0.1"
---
apiVersion: elasti.truefoundry.com/v1alpha1
kind: ElastiService
metadata:
  name: user-cache
  namespace: default
spec:
  service: user-cache
  # ... similar configuration
---
apiVersion: elasti.truefoundry.com/v1alpha1
kind: ElastiService
metadata:
  name: session-store
  namespace: redis
spec:
  service: session-store
  # ... similar configuration
---
apiVersion: elasti.truefoundry.com/v1alpha1
kind: ElastiService
metadata:
  name: auth-service
  namespace: security
spec:
  service: auth-service
  # ... similar configuration
```

### Step 3: Configure Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: api-gateway
  annotations:
    nginx.ingress.kubernetes.io/configuration-snippet: |
      proxy_set_header X-Scale-Up-Services "user-cache,session-store.redis,auth-service.security";
spec:
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: api-gateway
                port:
                  number: 8080
```

### Step 4: Verify

When a request arrives at `api.example.com`:
1. Resolver receives request with headers:
   - `Host: api-gateway.default.svc.cluster.local:8080`
   - `X-Scale-Up-Services: user-cache,session-store.redis,auth-service.security`
2. Resolver triggers scale-up for:
   - `api-gateway` in `default` namespace (main service)
   - `user-cache` in `default` namespace
   - `session-store` in `redis` namespace
   - `auth-service` in `security` namespace
3. All services scale concurrently from 0 to minReplicas
4. Request is queued and forwarded to `api-gateway` once ready

## Monitoring and Observability

### Logs

**Resolver logs** (Info level):
```
Scaling additional services from header
  count: 3
  main_service: api-gateway
  namespace: default
```

**Resolver logs** (Debug level):
```
Sending scale request for additional service
  service: user-cache
  namespace: default
```

**Resolver logs** (Warn level - validation failures):
```
Invalid service identifier in headerForScaleUp
  original_value: Invalid_Service
  parsed_service: Invalid_Service
  parsed_namespace: default
  header_name: X-Scale-Up-Services
```

**Resolver logs** (Warn level - oversized header):
```
headerForScaleUp exceeds recommended size limit
  size: 8500
  max_size: 8000
  header_name: X-Scale-Up-Services
```

### Metrics

Monitor the operator's existing metrics:
- `elasti_operator_target_scale_count` - tracks scale-up attempts
- `elasti_resolver_incoming_request_count` - tracks requests through resolver
- `elasti_resolver_host_extraction` - tracks host parsing (cache hits/misses/errors)

## Security Considerations

### RBAC

The operator requires permissions to scale deployments in all namespaces where services are defined. Ensure proper RBAC is configured:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: elasti-operator
rules:
  - apiGroups: ["apps"]
    resources: ["deployments", "statefulsets"]
    verbs: ["get", "list", "watch", "update", "patch"]
  - apiGroups: ["apps"]
    resources: ["deployments/scale", "statefulsets/scale"]
    verbs: ["get", "update", "patch"]
```

### Input Validation

- Service names are validated against Kubernetes DNS-1123 standard
- Invalid names are rejected (not processed)
- Malformed input doesn't crash the resolver
- Header size is monitored (warning at >8KB)

### Namespace Isolation

- Cross-namespace scaling is allowed by design
- Operator enforces Kubernetes RBAC for all scale operations
- Services can only be scaled if the operator has permissions in that namespace

### Denial of Service Prevention

- Header size warnings prevent extremely large headers
- Concurrent scale-up calls are bounded by the number of services in header
- Operator uses existing locking mechanism to prevent duplicate scale requests
- Each service scales independently (failure doesn't cascade)

## Performance Implications

### Latency

- Parsing header: <1ms (negligible)
- Launching goroutines: <1ms per service (concurrent)
- Scale-up RPC calls: ~10-50ms per service (concurrent)
- **Main request is not blocked** by additional service scale-ups

### Resource Usage

- Memory: ~1 KB per service in header (temporary)
- Goroutines: 1 per additional service (short-lived)
- Network: 1 HTTP POST per service to operator
- Operator: Existing lock mechanism prevents thundering herd

### Scalability

- Tested with up to 50 services in header
- Recommended limit: 20-30 services for optimal performance
- For very large dependency graphs (>50 services), consider:
  - Breaking into multiple entry points
  - Using service-to-service discovery
  - Implementing a warm-up endpoint

## Troubleshooting

### Services not scaling up

**Check 1**: Verify header is being set
```bash
kubectl logs -n elasti-system deployment/elasti-resolver -c resolver | grep "Scaling additional services"
```

**Check 2**: Verify service names are valid
```bash
kubectl logs -n elasti-system deployment/elasti-resolver -c resolver | grep "Invalid service identifier"
```

**Check 3**: Verify ElastiService CRDs exist
```bash
kubectl get elastiservice -A
```

**Check 4**: Verify operator permissions
```bash
kubectl auth can-i update deployments --as=system:serviceaccount:elasti-system:elasti-operator -n <namespace>
```

### Header not found

**Possible causes:**
1. Ingress/gateway not setting the header
2. Wrong header name in configuration
3. Header stripped by intermediate proxy

**Debug:**
```bash
# Check resolver configuration
kubectl get deployment -n elasti-system elasti-resolver -o yaml | grep HEADER_FOR_SCALE_UP

# Test with curl (if accessible)
curl -H "X-Scale-Up-Services: service1,service2" https://api.example.com
```

### High latency

**Check**: Are too many services in the header?
```bash
# Monitor header size warnings
kubectl logs -n elasti-system deployment/elasti-resolver -c resolver | grep "exceeds recommended size"
```

**Solution**: Reduce number of services or optimize service naming

## Backward Compatibility

This feature is **100% backward compatible**:
- If `headerForScaleUp` is not configured (empty string), feature is disabled
- If header is not present in requests, no additional services are scaled
- No changes to existing ElastiService CRD or operator behavior
- Main request processing is unchanged

## Comparison with Alternatives

| Approach | Pros | Cons |
|----------|------|------|
| **headerForScaleUp** | Simple, centralized, low latency | Limited to ~50 services, requires ingress config |
| **Service-to-service calls** | Natural dependencies, unlimited scale | Higher latency, more complex debugging |
| **Separate warm-up endpoint** | Explicit control, testable | Extra endpoint, requires client coordination |
| **Always-on services** | Zero latency | Higher cost, defeats scale-to-zero purpose |

## Best Practices

1. **Keep dependency lists short** (< 30 services)
2. **Use explicit namespaces** for cross-namespace dependencies
3. **Monitor header sizes** in resolver logs
4. **Test with realistic traffic** patterns
5. **Document dependencies** in your service architecture
6. **Set appropriate cooldown periods** in ElastiService CRDs
7. **Use consistent service naming** conventions
8. **Monitor scale-up metrics** for performance tuning

## Future Enhancements

Potential improvements for future versions:
- Support for multiple headers (primary + fallback)
- Weighted/prioritized service list (scale some before others)
- Dynamic service discovery from annotations
- Integration with service mesh observability
- Dashboard visualization of dependency graphs
