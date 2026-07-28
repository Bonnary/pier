package tui

import "slices"

// newAddPicker returns a multi-select Picker of services the user can add.
// Already-installed services are filtered out (idempotency contract: add
// is a no-op for installed services).
func newAddPicker(available, installed []string) *Picker {
	installedSet := make(map[string]bool, len(installed))
	for _, n := range installed {
		installedSet[n] = true
	}
	filtered := make([]string, 0, len(available))
	for _, n := range available {
		if !installedSet[n] {
			filtered = append(filtered, n)
		}
	}
	return NewMultiPicker("Services to add (space to toggle)", filtered, nil)
}

// newRemovePicker returns a multi-select Picker of currently installed
// services; the user picks which ones to remove.
func newRemovePicker(installed []string) *Picker {
	items := slices.Clone(installed)
	sortStrings(items)
	return NewMultiPicker("Services to remove (space to toggle)", items, nil)
}

func PickServicesToAdd(available, installed []string) ([]string, error) {
	p := newAddPicker(available, installed)
	if len(p.items) == 0 {
		return nil, nil
	}
	res, err := p.Run()
	if err != nil {
		return nil, err
	}
	if res.Aborted {
		return nil, ErrAborted
	}
	return res.Values, nil
}

func PickServicesToRemove(installed []string) ([]string, error) {
	p := newRemovePicker(installed)
	if len(p.items) == 0 {
		return nil, nil
	}
	res, err := p.Run()
	if err != nil {
		return nil, err
	}
	if res.Aborted {
		return nil, ErrAborted
	}
	return res.Values, nil
}

var ErrAborted = errAborted{}

type errAborted struct{}

func (errAborted) Error() string { return "aborted" }

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
