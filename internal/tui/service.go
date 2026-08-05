package tui

// presetIndices maps every item of available that appears in current
// to true — the pre-ticked set for the services picker.
func presetIndices(available, current []string) map[int]bool {
	presets := make(map[int]bool, len(current))
	for _, c := range current {
		for i, a := range available {
			if a == c {
				presets[i] = true
				break
			}
		}
	}
	return presets
}

// PickServices opens a multi-select Picker of every service in
// available with the services in current pre-ticked. Toggling adds
// and removes; enter returns the final selection. Returns ErrAborted
// (wrapped in the error) if the user hits q / Ctrl+C. Returns
// (nil, nil) when available is empty.
func PickServices(available, current []string) ([]string, error) {
	p := NewMultiPicker("Services (space to toggle)", available, presetIndices(available, current))
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

// ErrAborted is returned by PickServices when the user aborts the
// TUI. Use errors.Is to detect it; the CLI maps it to
// AbortedError().
var ErrAborted = errAborted{}

type errAborted struct{}

func (errAborted) Error() string { return "aborted" }
