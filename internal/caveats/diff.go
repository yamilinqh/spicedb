package caveats

import (
	"golang.org/x/exp/maps"

	"github.com/authzed/spicedb/internal/util"
	"github.com/authzed/spicedb/pkg/caveats/types"
	core "github.com/authzed/spicedb/pkg/proto/core/v1"
)

// DeltaType defines the type of caveat deltas.
type DeltaType string

const (
	// CaveatAdded indicates that the caveat was newly added/created.
	CaveatAdded DeltaType = "caveat-added"

	// CaveatRemoved indicates that the caveat was removed.
	CaveatRemoved DeltaType = "caveat-removed"

	// AddedParameter indicates that the parameter was added to the caveat.
	AddedParameter DeltaType = "added-parameter"

	// RemovedParameter indicates that the parameter was removed from the caveat.
	RemovedParameter DeltaType = "removed-parameter"

	// ParameterTypeChanged indicates that the type of the parameter was changed.
	ParameterTypeChanged DeltaType = "parameter-type-changed"
)

// Diff holds the diff between two caveats.
type Diff struct {
	existing *core.CaveatDefinition
	updated  *core.CaveatDefinition
	deltas   []Delta
}

// Deltas returns the deltas between the two caveats.
func (cd Diff) Deltas() []Delta {
	return cd.deltas
}

// Delta holds a single change of a caveat.
type Delta struct {
	// Type is the type of this delta.
	Type DeltaType

	// ParameterName is the name of the parameter to which this delta applies, if any.
	ParameterName string

	// PreviousType is the previous type of the parameter changed, if any.
	PreviousType *core.CaveatTypeReference

	// CurrentType is the current type of the parameter changed, if any.
	CurrentType *core.CaveatTypeReference
}

// DiffCaveats performs a diff between two caveat definitions. One or both of the definitions
// can be `nil`, which will be treated as an add/remove as applicable.
func DiffCaveats(existing *core.CaveatDefinition, updated *core.CaveatDefinition) (*Diff, error) {
	// Check for the caveats themselves.
	if existing == nil && updated == nil {
		return &Diff{existing, updated, []Delta{}}, nil
	}

	if existing != nil && updated == nil {
		return &Diff{
			existing: existing,
			updated:  updated,
			deltas: []Delta{
				{
					Type: CaveatRemoved,
				},
			},
		}, nil
	}

	if existing == nil && updated != nil {
		return &Diff{
			existing: existing,
			updated:  updated,
			deltas: []Delta{
				{
					Type: CaveatAdded,
				},
			},
		}, nil
	}

	var deltas []Delta
	existingParameterNames := util.NewSet[string](maps.Keys(existing.ParameterTypes)...)
	updatedParameterNames := util.NewSet[string](maps.Keys(updated.ParameterTypes)...)

	for _, removed := range existingParameterNames.Subtract(updatedParameterNames).AsSlice() {
		deltas = append(deltas, Delta{
			Type:          RemovedParameter,
			ParameterName: removed,
		})
	}

	for _, added := range updatedParameterNames.Subtract(existingParameterNames).AsSlice() {
		deltas = append(deltas, Delta{
			Type:          AddedParameter,
			ParameterName: added,
		})
	}

	for _, shared := range existingParameterNames.Intersect(updatedParameterNames).AsSlice() {
		existingParamType := existing.ParameterTypes[shared]
		updatedParamType := updated.ParameterTypes[shared]

		existingType, err := types.DecodeParameterType(existingParamType)
		if err != nil {
			return nil, err
		}

		updatedType, err := types.DecodeParameterType(updatedParamType)
		if err != nil {
			return nil, err
		}

		// Compare types.
		if existingType.String() != updatedType.String() {
			deltas = append(deltas, Delta{
				Type:          ParameterTypeChanged,
				ParameterName: shared,
				PreviousType:  existingParamType,
				CurrentType:   updatedParamType,
			})
		}
	}

	return &Diff{
		existing: existing,
		updated:  updated,
		deltas:   deltas,
	}, nil
}
