package tui

// PickEnv opens a single-select Picker over the given labels and
// returns the chosen index. start is the initially highlighted (and
// pre-ticked) label index. Returns -1 with a nil error when labels
// is empty, and ErrAborted when the user hits q / Ctrl+C.
func PickEnv(labels []string, start int) (int, error) {
	if len(labels) == 0 {
		return -1, nil
	}
	if start < 0 || start >= len(labels) {
		start = 0
	}
	p := NewSinglePicker("Env to bootstrap", labels, start)
	res, err := p.Run()
	if err != nil {
		return -1, err
	}
	if res.Aborted {
		return -1, ErrAborted
	}
	return res.Indices[0], nil
}
