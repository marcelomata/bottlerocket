package intent

import (
	"github.com/amazonlinux/thar/dogswatch/pkg/marker"
)

// TODO: encapsulate state machine-y handling. Callers should not have to
// reference marker to compare the needed state nor set the necessary response.

// Intent is a pseudo-Container of Labels and Annotations.
var _ marker.Container = (*Intent)(nil)

// Intent is the sole communicator of state progression and desired intentions
// for an Agent to act upon and to communicate its progress.
type Intent struct {
	// NodeName is the Resource name that addresses it.
	NodeName string
	// CurrentAction is the currently instructed action on the node.
	Wanted marker.NodeAction
	// CurrentState is the current action taken on the Node.
	Active marker.NodeAction
	// State is the current status or state of the Node reported, generally for
	// the Active action.
	State marker.NodeState
	// UpdateAvailable is the Node's status of having an update ready to apply.
	UpdateAvailable marker.NodeUpdate
}

// GetName returns the name of the Intent's target.
func (i *Intent) GetName() string {
	return i.NodeName
}

// GetAnnotations transposes the Intent into a map of Annotations suitable for
// adding to a Resource.
func (i *Intent) GetAnnotations() map[string]string {
	return map[string]string{
		marker.NodeActionWanted:      i.Wanted,
		marker.NodeActionActive:      i.Active,
		marker.NodeActionActiveState: i.State,
		marker.UpdateAvailableKey:    i.UpdateAvailable,
	}
}

// GetLabels transposes the Intent into a map of Labels suitable for adding to a
// Resource.
func (i *Intent) GetLabels() map[string]string {
	return map[string]string{
		marker.UpdateAvailableKey: i.UpdateAvailable,
	}
}

// Waiting reports true when a Node is prepared and waiting to make further
// commanded progress towards completing an update. This doesn't however
// indicate whether or not the intent reached a waiting state successfully or
// not. Utilize other checks to assert a combinational check.
func (i *Intent) Waiting() bool {
	var done bool
	switch i.State {
	case marker.NodeStateReady:
		// Ready for action, probably waiting for next command.
		done = true
	case "", marker.NodeStateUnknown:
		// Node is an unknown state or doesn't yet have a state. This is likely
		// because there hasn't been a requested action or because it hasn't yet
		// done something to warrant action.
		done = true
	case marker.NodeStateError:
		// Node errored and is waiting on a next step.
		done = true
	default:
		// The state is unclear, other predicates may better inform the caller
		// of the scenario its handling.
		done = false
	}
	return done
}

// Intrusive indicates that the intention will be intrusive if realized.
func (i *Intent) Intrusive() bool {
	rebooting := i.Wanted == marker.NodeActionRebootUpdate
	return rebooting
}

// Errored indicates that the intention was not realized and failed in attempt
// to do so.
func (i *Intent) Errored() bool {
	errored := i.State == marker.NodeStateError
	return errored
}

// Stuck intents are those that cannot be realized in their current state and
// should be unstuck.
func (i *Intent) Stuck() bool {
	// The state is inconsistent or isn't actually making progress
	inconsistentProgress := i.Active != i.Wanted && !i.InProgress()
	// The step in the forward direction doesn't make check out, its stuck.
	invalidProgress := i.Wanted != i.projectActive().Wanted

	return inconsistentProgress || invalidProgress
}

// Realized indicates that the Intent reached the intended state.
func (i *Intent) Realized() bool {
	realized := i.Wanted == i.Active
	successful := !i.Errored()
	return realized && successful && i.Waiting()
}

// InProgess reports true when the Intent is for a node that is making progress
// towards completing an update.
func (i *Intent) InProgress() bool {
	// The next step has been set, but not started yet so its waiting to be
	// started or realized.
	pendingNext := !i.Realized() && i.projectActive().Wanted == i.Wanted
	return i.isInProgress(i.Active) && pendingNext
}

// isInProgress indicates that the field provided is an action that may be able
// to make progress towards another state.
func (i *Intent) isInProgress(field marker.NodeAction) bool {
	switch field {
	case marker.NodeActionReset,
		marker.NodeActionStablize,
		marker.NodeActionPrepareUpdate,
		marker.NodeActionPerformUpdate,
		marker.NodeActionRebootUpdate:
		return true
	}
	return false
}

// HasUpdateAvailable indicates the Node has flagged itself as needing an
// update.
func (i *Intent) HasUpdateAvailable() bool {
	return i.UpdateAvailable == marker.NodeUpdateAvailable
}

// Needed indicates that the intent is needing progress made on it.
func (i *Intent) Actionable() bool {
	canProgress := (i.Waiting() || i.Realized()) && !i.Terminal()
	unknown := i.inUnknownState()
	stuck := i.Stuck()
	return canProgress || unknown || stuck
}

// Projected returns the n+1 step projection of a would-be Intent. It does not
// check whether or not the next step is *sane* given the current intent (ie:
// this will not error if the node has not actually completed a step).
func (i *Intent) Projected() *Intent {
	p := i.Clone()
	if p.inUnknownState() {
		p.reset()
	}
	p.Wanted, _ = calculateNext(p.Wanted)
	return p
}

func (i *Intent) projectActive() *Intent {
	prior := i.Clone()
	prior.Wanted = i.Active
	return prior.Projected()
}

func (i *Intent) inUnknownState() bool {
	return i.State == "" ||
		i.State == marker.NodeStateUnknown
}

// Terminal indicates that the intent has reached a terminal point in the
// progression, the intent will not make progress in anyway without outside
// state action.
func (i *Intent) Terminal() bool {
	next, err := calculateNext(i.Wanted)
	if err != nil {
		return false
	}
	// The next turn in the state machine is the same as the realized Wanted and
	// Active states, therefore we've reached a terminal point.
	atTerminal := next == i.Wanted && i.Wanted == i.Active
	return atTerminal
}

// Reset brings the intent back to the start of the progression where the intent
// may be able to resolve issues and fall into a valid state.
func (i *Intent) Reset() *Intent {
	p := i.Clone()
	p.reset()
	return p.Projected()
}

// reset reverts the Intent to its Origin point from which an Intent should be
// able to be driven to a Terminal point.
func (i *Intent) reset() {
	i.Wanted = marker.NodeActionUnknown
	i.Active = marker.NodeActionUnknown
	i.State = marker.NodeStateUnknown
}

// Clone returns a copy of the Intent to mutate independently of the source
// instance.
func (i Intent) Clone() *Intent {
	return Given(&i)
}

// Given determines the commuincated intent from a Node without projecting into
// its next steps.
func Given(input Input) *Intent {
	annos := input.GetAnnotations()

	intent := &Intent{
		NodeName:        input.GetName(),
		Active:          annos[marker.NodeActionActive],
		Wanted:          annos[marker.NodeActionWanted],
		State:           annos[marker.NodeActionActiveState],
		UpdateAvailable: annos[marker.UpdateAvailableKey],
	}

	return intent
}

// Input is a suitable container of data for interpreting an Intent from. This
// effectively is a subset of the kubernetes' v1.Node accessors, but is more
// succinct and scoped to the used accessors.
type Input interface {
	marker.Container
	// GetName returns the Input's addressable Name.
	GetName() string
}
