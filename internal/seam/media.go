package seam

// Images counts the image blocks a request carries: in its messages, and
// in the content of its tool results. A provider whose model cannot see is
// never sent a request where this is more than zero (P9-02).
func Images(r Request) int {
	n := 0
	for _, m := range r.Messages {
		n += imagesIn(m.Blocks)
	}
	return n
}

func imagesIn(blocks []Block) int {
	n := 0
	for _, b := range blocks {
		if b.Type == BlockImage {
			n++
		}
		n += imagesIn(b.Content)
	}
	return n
}
