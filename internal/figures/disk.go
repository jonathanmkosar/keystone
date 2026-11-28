package figures

// Disk is the volume that holds the data folder: everything on it, which
// says nothing of any one person. Since P15-02 only the admin account shows
// it (decision 0701).
type Disk struct {
	Used    int64  `json:"used"`
	Of      int64  `json:"of"`
	Percent int    `json:"percent"`
	Watch   int    `json:"watch"`
	State   string `json:"state"`
	Crossed bool   `json:"crossed"`
	Problem string `json:"problem,omitempty"`
}

// ReadDisk reads the volume that holds dir against a watch level in
// percent.
func ReadDisk(dir string, watch int) Disk {
	d := Disk{Watch: watch}
	if total, free, err := Volume(dir); err != nil {
		d.Problem = "the volume's size could not be read: " + err.Error()
	} else {
		d.Used, d.Of = int64(total-min(free, total)), int64(total)
	}
	d.Percent, d.State, d.Crossed = Judge(LimitDisk, d.Used, d.Of, d.Watch, d.Problem)
	return d
}
