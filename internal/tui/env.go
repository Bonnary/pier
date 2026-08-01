package tui

// PickEnv opens a single-select Picker over the given labels and
// returns the chosen index. Returns -1 with a nil error when labels
// is empty, and ErrAborted when the user hits q / Ctrl+C.
func PickEnv(labels []string) (int, error) {
	if len(labels) == 0 {
		return -1, nil
	}
	p := NewSinglePicker("Env to bootstrap", labels, 0)
	res, err := p.Run()
	if err != nil {
		return -1, err
	}
	if res.Aborted {
		return -1, ErrAborted
	}
	return res.Indices[0], nil
}
