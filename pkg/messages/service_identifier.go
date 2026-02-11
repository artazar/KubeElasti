package messages

// ServiceIdentifier represents a Kubernetes service to be scaled
type ServiceIdentifier struct {
	Service   string
	Namespace string
}
