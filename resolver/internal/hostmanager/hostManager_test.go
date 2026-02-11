package hostmanager

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/truefoundry/elasti/pkg/messages"
	"go.uber.org/zap"
)

// Type alias for convenience in tests
type ServiceIdentifier = messages.ServiceIdentifier

func TestGetHost(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	hm := NewHostManager(logger, 10*time.Second, "X-Envoy-Decorator-Operation", "")

	tests := []struct {
		name          string
		req           *http.Request
		expectedHost  *messages.Host
		expectedError bool
	}{
		{
			name: "Host in header",
			req: &http.Request{
				Host: "target.com",
				Header: http.Header{
					"X-Envoy-Decorator-Operation": []string{"service.namespace.svc.cluster.local:8080/test/*"},
				},
			},
			expectedHost: &messages.Host{
				IncomingHost:   "service.namespace.svc.cluster.local:8080/test/*",
				Namespace:      "namespace",
				SourceService:  "service",
				TargetService:  "elasti-service-pvt-9df6b026a8",
				SourceHost:     "http://service.namespace.svc.cluster.local:8080",
				TargetHost:     "http://elasti-service-pvt-9df6b026a8.namespace.svc.cluster.local:8080",
				TrafficAllowed: true,
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, err := hm.GetHost(tt.req)
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectedHost, host)
		})
	}
}

func TestParseAdditionalServices(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	tests := []struct {
		name             string
		headerForScaleUp string
		headerValue      string
		defaultNamespace string
		expected         []ServiceIdentifier
	}{
		{
			name:             "Empty header name - feature disabled",
			headerForScaleUp: "",
			headerValue:      "service1,service2",
			defaultNamespace: "default",
			expected:         nil,
		},
		{
			name:             "Empty header value",
			headerForScaleUp: "X-Scale-Up",
			headerValue:      "",
			defaultNamespace: "default",
			expected:         nil,
		},
		{
			name:             "Single service without namespace",
			headerForScaleUp: "X-Scale-Up",
			headerValue:      "my-service",
			defaultNamespace: "default",
			expected: []ServiceIdentifier{
				{Service: "my-service", Namespace: "default"},
			},
		},
		{
			name:             "Single service with namespace",
			headerForScaleUp: "X-Scale-Up",
			headerValue:      "my-service.production",
			defaultNamespace: "default",
			expected: []ServiceIdentifier{
				{Service: "my-service", Namespace: "production"},
			},
		},
		{
			name:             "Multiple services mixed format",
			headerForScaleUp: "X-Scale-Up",
			headerValue:      "service1,service2.namespace2,service3",
			defaultNamespace: "default",
			expected: []ServiceIdentifier{
				{Service: "service1", Namespace: "default"},
				{Service: "service2", Namespace: "namespace2"},
				{Service: "service3", Namespace: "default"},
			},
		},
		{
			name:             "Services with whitespace",
			headerForScaleUp: "X-Scale-Up",
			headerValue:      " service1 , service2.ns2 , service3 ",
			defaultNamespace: "default",
			expected: []ServiceIdentifier{
				{Service: "service1", Namespace: "default"},
				{Service: "service2", Namespace: "ns2"},
				{Service: "service3", Namespace: "default"},
			},
		},
		{
			name:             "Empty entries ignored",
			headerForScaleUp: "X-Scale-Up",
			headerValue:      "service1,,service2,",
			defaultNamespace: "default",
			expected: []ServiceIdentifier{
				{Service: "service1", Namespace: "default"},
				{Service: "service2", Namespace: "default"},
			},
		},
		{
			name:             "Invalid service names skipped",
			headerForScaleUp: "X-Scale-Up",
			headerValue:      "valid-service,Invalid_Service,another-valid",
			defaultNamespace: "default",
			expected: []ServiceIdentifier{
				{Service: "valid-service", Namespace: "default"},
				{Service: "another-valid", Namespace: "default"},
			},
		},
		{
			name:             "Kubernetes naming constraints",
			headerForScaleUp: "X-Scale-Up",
			headerValue:      "my-service-123,service.namespace-123,svc",
			defaultNamespace: "ns",
			expected: []ServiceIdentifier{
				{Service: "my-service-123", Namespace: "ns"},
				{Service: "service", Namespace: "namespace-123"},
				{Service: "svc", Namespace: "ns"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hm := NewHostManager(logger, 10*time.Second, "X-Envoy-Decorator-Operation", tt.headerForScaleUp)

			req := &http.Request{
				Header: http.Header{},
			}
			if tt.headerValue != "" && tt.headerForScaleUp != "" {
				req.Header.Set(tt.headerForScaleUp, tt.headerValue)
			}

			result := hm.ParseAdditionalServices(req, tt.defaultNamespace)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsValidKubernetesName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid simple name", "my-service", true},
		{"valid with numbers", "service-123", true},
		{"valid single char", "a", true},
		{"valid all lowercase", "myservice", true},
		{"empty string", "", false},
		{"starts with dash", "-service", false},
		{"ends with dash", "service-", false},
		{"uppercase", "MyService", false},
		{"underscore", "my_service", false},
		{"special chars", "service@123", false},
		{"too long", string(make([]byte, 254)), false},
		{"starts with number", "1service", true},
		{"ends with number", "service1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidKubernetesName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
