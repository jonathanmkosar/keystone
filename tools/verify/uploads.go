package main

// Uploads (P9-02): the files and images a person adds to a chat. The
// package has a doc, and serves routes.
func init() {
	componentDocs["internal/uploads"] = "docs/architecture/uploads.md"
	networkAllowed["internal/uploads"] = "the upload routes' handlers, served by the server, and an image known by its bytes with net/http's sniffer; it opens no connection"
}
