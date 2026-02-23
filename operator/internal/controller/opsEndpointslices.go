package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/truefoundry/elasti/pkg/config"
	"github.com/truefoundry/elasti/pkg/utils"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ElastiServiceReconciler) getIPsForResolver(ctx context.Context) ([]string, error) {
	resolverSlices := &networkingv1.EndpointSliceList{}
	if err := r.List(ctx, resolverSlices, client.MatchingLabels{
		"kubernetes.io/service-name": config.GetResolverConfig().ServiceName,
	}); err != nil {
		r.Logger.Error("Failed to get Resolver endpoint slices", zap.Error(err))
		return nil, fmt.Errorf("getIPsForResolver: %w", err)
	}
	var resolverPodIPs []string
	for _, endpointSlice := range resolverSlices.Items {
		for _, endpoint := range endpointSlice.Endpoints {
			resolverPodIPs = append(resolverPodIPs, endpoint.Addresses...)
		}
	}
	if len(resolverPodIPs) == 0 {
		return nil, ErrNoResolverPodFound
	}
	return resolverPodIPs, nil
}

// verifyNaturalEndpointsliceHealthy checks if the natural endpointslice exists and has ready endpoints
func (r *ElastiServiceReconciler) verifyNaturalEndpointsliceHealthy(ctx context.Context, serviceNamespacedName types.NamespacedName) (bool, error) {
	// List all endpointslices for the service
	endpointSliceList := &networkingv1.EndpointSliceList{}
	if err := r.List(ctx, endpointSliceList, client.InNamespace(serviceNamespacedName.Namespace), client.MatchingLabels{
		"kubernetes.io/service-name": serviceNamespacedName.Name,
	}); err != nil {
		r.Logger.Error("Failed to list endpointslices", zap.String("service", serviceNamespacedName.String()), zap.Error(err))
		return false, fmt.Errorf("failed to list endpointslices: %w", err)
	}

	// Filter out the resolver endpointslice (we want the natural one created by Kubernetes)
	resolverEndpointSliceName := utils.GetEndpointSliceToResolverName(serviceNamespacedName.Name)

	for _, slice := range endpointSliceList.Items {
		// Skip the resolver endpointslice
		if slice.Name == resolverEndpointSliceName {
			continue
		}

		// Check if this natural endpointslice has ready endpoints
		readyEndpointCount := 0
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && *endpoint.Conditions.Ready {
				readyEndpointCount++
			}
		}

		if readyEndpointCount > 0 {
			r.Logger.Info("Natural endpointslice is healthy",
				zap.String("service", serviceNamespacedName.String()),
				zap.String("endpointslice", slice.Name),
				zap.Int("ready_endpoints", readyEndpointCount))
			return true, nil
		}
	}

	r.Logger.Debug("No healthy natural endpointslice found",
		zap.String("service", serviceNamespacedName.String()))
	return false, nil
}

// deleteEndpointsliceToResolverWithBlueGreen implements blue-green strategy for zero-downtime switching
// It verifies natural endpointslice is healthy, waits for propagation, then deletes resolver endpointslice
func (r *ElastiServiceReconciler) deleteEndpointsliceToResolverWithBlueGreen(ctx context.Context, serviceNamespacedName types.NamespacedName, propagationDelay time.Duration) error {
	resolverSliceName := types.NamespacedName{
		Name:      utils.GetEndpointSliceToResolverName(serviceNamespacedName.Name),
		Namespace: serviceNamespacedName.Namespace,
	}

	endpointSlice := &networkingv1.EndpointSlice{}
	if err := r.Get(ctx, resolverSliceName, endpointSlice); err != nil && !errors.IsNotFound(err) {
		r.Logger.Error("Failed to get resolver endpointslice", zap.String("service", serviceNamespacedName.String()), zap.Error(err))
		return fmt.Errorf("failed to get endpointslice: %w", err)
	} else if errors.IsNotFound(err) {
		r.Logger.Debug("Resolver endpointslice not found, already deleted", zap.String("service", serviceNamespacedName.String()))
		return nil
	}

	// Step 1: Verify natural endpointslice exists and is healthy
	r.Logger.Info("Blue-Green Switch: Step 1 - Verifying natural endpointslice is healthy",
		zap.String("service", serviceNamespacedName.String()))

	healthy, err := r.verifyNaturalEndpointsliceHealthy(ctx, serviceNamespacedName)
	if err != nil {
		return fmt.Errorf("failed to verify natural endpointslice: %w", err)
	}

	if !healthy {
		r.Logger.Warn("Natural endpointslice is not healthy yet, skipping blue-green switch",
			zap.String("service", serviceNamespacedName.String()),
			zap.String("reason", "Will retry on next reconciliation"))
		return fmt.Errorf("natural endpointslice not ready yet for service %s", serviceNamespacedName.String())
	}

	// Step 2: Wait for propagation delay to ensure kube-proxy/service mesh has updated routing tables
	r.Logger.Info("Blue-Green Switch: Step 2 - Waiting for endpointslice propagation",
		zap.String("service", serviceNamespacedName.String()),
		zap.Duration("propagation_delay", propagationDelay))

	time.Sleep(propagationDelay)

	// Step 3: Delete resolver endpointslice
	r.Logger.Info("Blue-Green Switch: Step 3 - Deleting resolver endpointslice",
		zap.String("service", serviceNamespacedName.String()))

	if err := r.Delete(ctx, endpointSlice); err != nil {
		return fmt.Errorf("failed to delete endpointslice: %w", err)
	}

	r.Logger.Info("Blue-Green Switch: Complete - Zero-downtime transition successful",
		zap.String("service", serviceNamespacedName.String()))

	return nil
}

// deleteEndpointsliceToResolver is the legacy immediate deletion method (no blue-green)
// Kept for backward compatibility but not recommended for production use
func (r *ElastiServiceReconciler) deleteEndpointsliceToResolver(ctx context.Context, serviceNamespacedName types.NamespacedName) error {
	endpointSlice := &networkingv1.EndpointSlice{}
	serviceNamespacedName.Name = utils.GetEndpointSliceToResolverName(serviceNamespacedName.Name)
	if err := r.Get(ctx, serviceNamespacedName, endpointSlice); err != nil && !errors.IsNotFound(err) {
		r.Logger.Error("Failed to get endpoint slice", zap.String("service", serviceNamespacedName.String()), zap.Error(err))
		return fmt.Errorf("failed to get endpointslice: %w", err)
	} else if errors.IsNotFound(err) {
		return nil
	}

	if err := r.Delete(ctx, endpointSlice); err != nil {
		return fmt.Errorf("failed to delete endpointslice: %w", err)
	}
	return nil
}

func (r *ElastiServiceReconciler) createOrUpdateEndpointsliceToResolver(ctx context.Context, service *v1.Service) error {
	resolverPodIPs, err := r.getIPsForResolver(ctx)
	if err != nil {
		r.Logger.Error("Failed to get IPs for Resolver", zap.String("service", service.Name), zap.Error(err))
		return err
	}

	// NOTE: Suggestion is to give it a random name in end, to avoid any conflicts, which is rare, but possible.
	// In case of random name, we need to store the name in CRD. Right now, we provide a deterministic hashed name.
	newEndpointsliceToResolverName := utils.GetEndpointSliceToResolverName(service.Name)
	EndpointsliceNamespacedName := types.NamespacedName{
		Name:      newEndpointsliceToResolverName,
		Namespace: service.Namespace,
	}

	isResolverSliceFound := false
	sliceToResolver := &networkingv1.EndpointSlice{}
	if err := r.Get(ctx, EndpointsliceNamespacedName, sliceToResolver); err != nil && !errors.IsNotFound(err) {
		r.Logger.Debug("Error getting a endpoint slice to Resolver", zap.String("endpointslice", EndpointsliceNamespacedName.String()), zap.Error(err))
		return fmt.Errorf("createOrUpdateEndpointsliceToResolver: %w", err)
	} else if errors.IsNotFound(err) {
		// TODO: This can be handled better
		// This is a similar case as seen in resolver informer
		// We can handler this with the same logic as that
		isResolverSliceFound = false
		r.Logger.Debug("EndpointSlice not found, will try creating one", zap.String("endpointslice", EndpointsliceNamespacedName.String()))
	} else {
		isResolverSliceFound = true
		r.Logger.Debug("EndpointSlice Found", zap.String("endpointslice", EndpointsliceNamespacedName.String()))
	}

	newEndpointSlice := &networkingv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      newEndpointsliceToResolverName,
			Namespace: service.Namespace,
			Labels: map[string]string{
				"kubernetes.io/service-name": service.Name,
			},
		},
		AddressType: networkingv1.AddressTypeIPv4,
		Ports: []networkingv1.EndpointPort{
			{
				Name:     ptr.To(service.Spec.Ports[0].Name),
				Protocol: ptr.To(v1.ProtocolTCP),
				// Make this dynamic too
				Port: ptr.To(config.GetResolverConfig().ReverseProxyPort),
			},
		},
	}

	// sliceToResolver.DeepCopy()

	for _, ip := range resolverPodIPs {
		newEndpointSlice.Endpoints = append(newEndpointSlice.Endpoints, networkingv1.Endpoint{
			Addresses: []string{ip},
		})
	}

	if isResolverSliceFound {
		if err := r.Update(ctx, newEndpointSlice); err != nil {
			r.Logger.Error("failed to update sliceToResolver", zap.String("endpointslice", EndpointsliceNamespacedName.String()), zap.Error(err))
			return fmt.Errorf("createOrUpdateEndpointsliceToResolver: %w", err)
		}
		r.Logger.Info("EndpointSlice updated successfully", zap.String("endpointslice", EndpointsliceNamespacedName.String()))
	} else {
		// TODOS: Make sure the private service is owned by the ElastiService
		if err := r.Create(ctx, newEndpointSlice); err != nil {
			r.Logger.Error("failed to create sliceToResolver", zap.String("endpointslice", EndpointsliceNamespacedName.String()), zap.Error(err))
			return fmt.Errorf("createOrUpdateEndpointsliceToResolver: %w", err)
		}
		r.Logger.Info("EndpointSlice created successfully", zap.String("endpointslice", EndpointsliceNamespacedName.String()))
	}

	return nil
}
