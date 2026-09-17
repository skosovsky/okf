package mutation

import "github.com/skosovsky/okf/store"

// v02OperationDescriptor is the mutation-side closed union for operations
// introduced by the v0.2 contract. Its name is the canonical operation tag
// used by presentation diagnostics and normalized plan details.
type v02OperationDescriptor struct {
	name   string
	invoke func(*planState, store.Operation, string) (bool, error)
}

func describeV02Operation[T store.Operation](name string, apply func(*planState, T, string) error) v02OperationDescriptor {
	return v02OperationDescriptor{
		name: name,
		invoke: func(state *planState, operation store.Operation, canonicalName string) (bool, error) {
			exact, ok := operation.(T)
			if !ok {
				return false, nil
			}
			return true, apply(state, exact, canonicalName)
		},
	}
}

var v02OperationDescriptors = [...]v02OperationDescriptor{
	describeV02Operation("set_generated", (*planState).setGenerated),
	describeV02Operation("ensure_verification", (*planState).ensureVerification),
	describeV02Operation("remove_verification", (*planState).removeVerification),
	describeV02Operation("put_source", (*planState).putSource),
	describeV02Operation("remove_source", (*planState).removeSource),
	describeV02Operation("set_usage_window", (*planState).setUsageWindow),
	describeV02Operation("set_lifecycle", (*planState).setLifecycle),
	describeV02Operation("put_attested_computation", (*planState).putAttestedComputation),
	describeV02Operation("set_bundle_version", (*planState).setBundleVersion),
}

func applyV02OperationDescriptor(state *planState, operation store.Operation) (bool, error) {
	for _, descriptor := range v02OperationDescriptors {
		if handled, err := descriptor.invoke(state, operation, descriptor.name); handled {
			return true, err
		}
	}
	return false, nil
}
