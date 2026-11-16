package control

import (
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
)

// DeliveryHistoryIn reads the project's finished work as a planning lane reads it (FR-EST-12): from the project's own
// delivery records under the estimate policy in force, with each repository's name by its ID. The pack's history item
// and the API's read of it are this one function, so a person sees what the session sees. It reads nothing of the
// installation pool: the pool answers only the last tier of an estimate (FR-EST-13).
func DeliveryHistoryIn(policies ports.PolicyReader, deliveries ports.DeliveryReader, repositories ports.RepositoryReader) (delivery.History, map[string]string, error) {
	policy, err := estimatePolicyIn(policies)
	if err != nil {
		return delivery.History{}, nil, err
	}
	records, err := deliveries.Records()
	if err != nil {
		return delivery.History{}, nil, err
	}
	held, err := repositories.Repositories()
	if err != nil {
		return delivery.History{}, nil, err
	}
	names := map[string]string{}
	for _, repository := range held {
		names[repository.ID] = repository.Name
	}
	return delivery.HistoryOf(records, policy), names, nil
}
