package bundle

import (
	"context"
	"errors"
	"fmt"
)

var errPublicationEnginePlanInvalid = errors.New("publication engine plan invalid")

// publicationEngineOutcome is the primitive's receipt payload and its one
// policy-neutral durability fact. Adapters alone decide when their
// primitive has crossed a canonical mutation boundary.
type publicationEngineOutcome[Receipt any] struct {
	receipt           Receipt
	canonicalMutation bool
}

// publicationEngineResult preserves every primitive receipt in plan order,
// including a receipt returned with an execution error after canonical
// mutation. The accompanying error aggregates convergence failures in the
// same deterministic order in which callbacks ran.
type publicationEngineResult[Receipt any] struct {
	receipts          []Receipt
	canonicalMutation bool
}

// publicationEngineStep describes one already-selected core primitive. The
// engine never chooses a step, interprets a receipt, or retries a primitive.
type publicationEngineStep[Snapshot, Receipt any] struct {
	name       string
	revalidate func(context.Context, Snapshot) error
	primitive  func(context.Context, Snapshot) (publicationEngineOutcome[Receipt], error)
	receipt    func(context.Context, Snapshot, Receipt) error
	postverify func(context.Context, Snapshot, Receipt) error
}

// publicationEnginePlan owns its ordered steps and snapshot. cloneSnapshot is
// part of the adapter contract so the engine can give every callback an
// isolated observation without knowing the snapshot representation.
type publicationEnginePlan[Snapshot, Receipt any] struct {
	snapshot      Snapshot
	cloneSnapshot func(Snapshot) Snapshot
	cloneReceipt  func(Receipt) Receipt
	steps         []publicationEngineStep[Snapshot, Receipt]
}

func newPublicationEnginePlan[Snapshot, Receipt any](
	snapshot Snapshot,
	cloneSnapshot func(Snapshot) Snapshot,
	cloneReceipt func(Receipt) Receipt,
	steps []publicationEngineStep[Snapshot, Receipt],
) (publicationEnginePlan[Snapshot, Receipt], error) {
	if cloneSnapshot == nil {
		return publicationEnginePlan[Snapshot, Receipt]{},
			fmt.Errorf("%w: snapshot clone is unavailable", errPublicationEnginePlanInvalid)
	}
	if cloneReceipt == nil {
		return publicationEnginePlan[Snapshot, Receipt]{},
			fmt.Errorf("%w: receipt clone is unavailable", errPublicationEnginePlanInvalid)
	}
	plan := publicationEnginePlan[Snapshot, Receipt]{
		snapshot:      cloneSnapshot(snapshot),
		cloneSnapshot: cloneSnapshot,
		cloneReceipt:  cloneReceipt,
		steps:         append([]publicationEngineStep[Snapshot, Receipt](nil), steps...),
	}
	if err := validatePublicationEnginePlan(plan); err != nil {
		return publicationEnginePlan[Snapshot, Receipt]{}, err
	}
	return plan, nil
}

func validatePublicationEnginePlan[Snapshot, Receipt any](
	plan publicationEnginePlan[Snapshot, Receipt],
) error {
	invalid := func(reason string) error {
		return fmt.Errorf("%w: %s", errPublicationEnginePlanInvalid, reason)
	}
	if plan.cloneSnapshot == nil {
		return invalid("snapshot clone is unavailable")
	}
	if plan.cloneReceipt == nil {
		return invalid("receipt clone is unavailable")
	}
	if len(plan.steps) == 0 {
		return invalid("ordered step list is empty")
	}
	names := make(map[string]struct{}, len(plan.steps))
	for position, step := range plan.steps {
		if step.name == "" {
			return invalid(fmt.Sprintf("step %d has no name", position))
		}
		if _, duplicate := names[step.name]; duplicate {
			return invalid(fmt.Sprintf("step name %q is duplicated", step.name))
		}
		names[step.name] = struct{}{}
		switch {
		case step.revalidate == nil:
			return invalid(fmt.Sprintf("step %q has no snapshot revalidation", step.name))
		case step.primitive == nil:
			return invalid(fmt.Sprintf("step %q has no primitive", step.name))
		case step.receipt == nil:
			return invalid(fmt.Sprintf("step %q has no receipt", step.name))
		case step.postverify == nil:
			return invalid(fmt.Sprintf("step %q has no postverification", step.name))
		}
	}
	return nil
}

func runPublicationEngine[Snapshot, Receipt any](
	ctx context.Context,
	plan publicationEnginePlan[Snapshot, Receipt],
) (publicationEngineResult[Receipt], error) {
	result := publicationEngineResult[Receipt]{}
	zeroResult := publicationEngineResult[Receipt]{}
	if ctx == nil {
		return result, fmt.Errorf("%w: context is nil", errPublicationEnginePlanInvalid)
	}
	if err := validatePublicationEnginePlan(plan); err != nil {
		return result, err
	}
	operationContext := ctx
	convergenceErrors := []error{}
	checkCancellation := func() error {
		if result.canonicalMutation {
			return nil
		}
		return operationContext.Err()
	}
	clone := func() Snapshot {
		return plan.cloneSnapshot(plan.snapshot)
	}
	for _, step := range plan.steps {
		if err := checkCancellation(); err != nil {
			return zeroResult, err
		}
		if err := step.revalidate(operationContext, clone()); err != nil {
			if !result.canonicalMutation {
				return zeroResult, err
			}
			convergenceErrors = append(convergenceErrors, err)
		}
		if err := checkCancellation(); err != nil {
			return zeroResult, err
		}
		outcome, primitiveErr := step.primitive(operationContext, clone())
		if outcome.canonicalMutation && !result.canonicalMutation {
			result.canonicalMutation = true
			operationContext = context.WithoutCancel(operationContext)
		}
		if primitiveErr != nil {
			if !result.canonicalMutation {
				return zeroResult, primitiveErr
			}
			convergenceErrors = append(convergenceErrors, primitiveErr)
		}
		ownedReceipt := plan.cloneReceipt(outcome.receipt)
		result.receipts = append(result.receipts, ownedReceipt)
		if err := checkCancellation(); err != nil {
			return zeroResult, err
		}
		if err := step.receipt(operationContext, clone(), plan.cloneReceipt(ownedReceipt)); err != nil {
			if !result.canonicalMutation {
				return zeroResult, err
			}
			convergenceErrors = append(convergenceErrors, err)
		}
		if err := checkCancellation(); err != nil {
			return zeroResult, err
		}
		if err := step.postverify(operationContext, clone(), plan.cloneReceipt(ownedReceipt)); err != nil {
			if !result.canonicalMutation {
				return zeroResult, err
			}
			convergenceErrors = append(convergenceErrors, err)
		}
	}
	return result, errors.Join(convergenceErrors...)
}
