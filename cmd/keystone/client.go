package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/settings"
)

// The command-line client. It is a client of the HTTP API like the web
// interface, with no path of its own to the data folder: it signs in, keeps
// its session token in a file in the user's profile, and sends the token
// back in the same cookie a browser would.

// sessionFileEnv names another place for the client's session file.
const sessionFileEnv = "KEYSTONE_SESSION_FILE"

// savedSession is the client's session file.
type savedSession struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

// client is one command's connection to a server.
type client struct {
	server string
	file   string
	saved  savedSession
	http   *http.Client
}

// newClient reads the flags every client command takes, and the session
// file. It returns the arguments that are left.
func newClient(name string, args []string, errw io.Writer) (*client, []string, error) {
	fl := flag.NewFlagSet(name, flag.ContinueOnError)
	fl.SetOutput(errw)
	server := fl.String("server", "", "the server's address, such as http://127.0.0.1:7900; the one last signed in to unless given")
	file := fl.String("session-file", "", "where the session is kept; a file in your profile unless given")
	if err := fl.Parse(args); err != nil {
		return nil, nil, err
	}
	c := &client{file: *file, http: &http.Client{}}
	if c.file == "" {
		c.file = os.Getenv(sessionFileEnv)
	}
	if c.file == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, nil, err
		}
		c.file = filepath.Join(dir, "keystone", "session.json")
	}
	if raw, err := os.ReadFile(c.file); err == nil {
		if err := json.Unmarshal(raw, &c.saved); err != nil {
			return nil, nil, fmt.Errorf("%s is not a session file: delete it and sign in again", c.file)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	switch {
	case *server != "":
		c.server = *server
	case c.saved.Server != "":
		c.server = c.saved.Server
	default:
		reg, err := settings.Registry()
		if err != nil {
			return nil, nil, err
		}
		address, err := setting(reg, "server.address")
		if err != nil {
			return nil, nil, err
		}
		c.server = "http://" + address
	}
	c.server = strings.TrimRight(c.server, "/")
	return c, fl.Args(), nil
}

// do makes one call. The saved token is sent only to the server it was
// given by.
func (c *client) do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.server+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.saved.Token != "" && c.saved.Server == c.server {
		req.AddCookie(&http.Cookie{Name: api.CookieName, Value: c.saved.Token})
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s did not answer. Is keystone serve running? (%w)", c.server, err)
	}
	return resp, nil
}

// refusal turns an answer that is not a success into an error that says
// what the server said.
func refusal(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var e api.ErrorBody
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return fmt.Errorf("%s (%d %s, request %s)", e.Error, resp.StatusCode, e.Code, e.RequestID)
	}
	return fmt.Errorf("the server answered %s", resp.Status)
}

// call makes one call and reads its JSON answer into reply.
func (c *client) call(method, path string, body, reply any) error {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(raw)
	}
	resp, err := c.do(method, path, r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return refusal(resp)
	}
	if reply == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(reply)
}

func (c *client) save() error {
	if err := os.MkdirAll(filepath.Dir(c.file), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c.saved, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.file, append(raw, '\n'), 0o600)
}

// runClient carries out the commands that talk to a running server.
func runClient(name string, args []string, in io.Reader, out, errw io.Writer) int {
	c, rest, err := newClient(name, args, errw)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(errw, "keystone:", err)
		return 2
	}
	switch name {
	case "people":
		err = clientPeople(c, out)
	case "signin":
		if len(rest) != 1 {
			fmt.Fprintln(errw, "usage: keystone signin [--server <address>] <person-id>   (the password is read from standard input)")
			return 2
		}
		err = clientSignIn(c, rest[0], in, out, errw)
	case "signout":
		err = clientSignOut(c, out)
	case "whoami":
		err = clientWhoAmI(c, out)
	case "api":
		if len(rest) < 2 || len(rest) > 3 {
			fmt.Fprintln(errw, "usage: keystone api [--server <address>] <METHOD> <path> [<json body> | -]   (- reads the body from standard input)")
			return 2
		}
		err = clientAPI(c, rest, in, out)
	}
	if err != nil {
		fmt.Fprintln(errw, "keystone:", err)
		return 1
	}
	return 0
}

func clientPeople(c *client, out io.Writer) error {
	var reply api.PeopleReply
	if err := c.call("GET", api.Prefix+"signin/people", nil, &reply); err != nil {
		return err
	}
	if !reply.Listed {
		fmt.Fprintln(out, "This install does not list its people. Sign in with your own id.")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME")
	for _, p := range reply.People {
		fmt.Fprintf(w, "%s\t%s\n", p.ID, p.Name)
	}
	return w.Flush()
}

func clientSignIn(c *client, id string, in io.Reader, out, errw io.Writer) error {
	if f, ok := in.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			fmt.Fprintln(errw, "Type your password and press Enter. It shows on the screen as you type.")
		}
	}
	line, err := bufio.NewReader(io.LimitReader(in, 1<<16)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	password := strings.TrimRight(strings.TrimPrefix(line, "Feff"), "\r\n")
	if password == "" {
		return errors.New("no password was given on standard input")
	}
	raw, err := json.Marshal(api.SignInBody{Tenant: id, Password: password})
	if err != nil {
		return err
	}
	// A session for another server is not sent along with a sign-in.
	c.saved = savedSession{}
	resp, err := c.do("POST", api.Prefix+"session", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return refusal(resp)
	}
	var reply api.SessionReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return err
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == api.CookieName {
			c.saved = savedSession{Server: c.server, Token: ck.Value}
		}
	}
	if c.saved.Token == "" {
		return errors.New("the server signed in and sent no session")
	}
	if err := c.save(); err != nil {
		return err
	}
	fmt.Fprintf(out, "Signed in to %s as %s (%s) until %s.\n", c.server, reply.Person.Name, reply.Person.ID, reply.Session.Expires.Local().Format("January 2, 2006"))
	return nil
}

func clientSignOut(c *client, out io.Writer) error {
	if c.saved.Token == "" {
		fmt.Fprintln(out, "Not signed in.")
		return nil
	}
	c.server = c.saved.Server
	err := c.call("DELETE", api.Prefix+"session", nil, nil)
	// The file goes whatever the server said: a session the server has
	// already ended is signed out too.
	if rmErr := os.Remove(c.file); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return rmErr
	}
	if err != nil {
		fmt.Fprintf(out, "Signed out here. The server said: %v\n", err)
		return nil
	}
	fmt.Fprintf(out, "Signed out of %s.\n", c.server)
	return nil
}

func clientWhoAmI(c *client, out io.Writer) error {
	var reply api.SessionReply
	if err := c.call("GET", api.Prefix+"session", nil, &reply); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s (%s) on %s, signed in by %s on %s, until %s.\n", reply.Person.Name, reply.Person.ID, c.server,
		reply.Session.Method, reply.Session.Created.Local().Format("January 2, 2006"), reply.Session.Expires.Local().Format("January 2, 2006"))
	return nil
}

// clientAPI makes any call and prints the answer: JSON indented, and a
// stream of server-sent events line by line as it arrives.
func clientAPI(c *client, args []string, in io.Reader, out io.Writer) error {
	method, path := strings.ToUpper(args[0]), args[1]
	if !strings.HasPrefix(path, "/") {
		path = api.Prefix + path
	}
	var body io.Reader
	if len(args) == 3 {
		body = strings.NewReader(args[2])
		if args[2] == "-" {
			body = in
		}
	}
	resp, err := c.do(method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return refusal(resp)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		lines := bufio.NewReader(resp.Body)
		for {
			line, err := lines.ReadString('\n')
			if line != "" {
				io.WriteString(out, line)
			}
			if err != nil {
				return nil // the stream ended
			}
		}
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, bytes.TrimSpace(raw), "", "  ") == nil {
		raw = pretty.Bytes()
	}
	out.Write(raw)
	fmt.Fprintln(out)
	return nil
}

// runOpenAPI prints the OpenAPI document, or writes it to a file. It needs
// no server: the document is built from the routes compiled into the binary.
func runOpenAPI(args []string, out, errw io.Writer) int {
	fl := flag.NewFlagSet("openapi", flag.ContinueOnError)
	fl.SetOutput(errw)
	to := fl.String("out", "", "write the document to this file instead of printing it")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	doc, err := app.OpenAPI()
	if err == nil {
		if *to == "" {
			_, err = out.Write(doc)
		} else {
			err = os.WriteFile(*to, doc, 0o644)
		}
	}
	if err != nil {
		fmt.Fprintln(errw, "keystone:", err)
		return 1
	}
	return 0
}
