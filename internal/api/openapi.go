package api

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"time"
)

// The OpenAPI document is built from the list of routes, so it cannot say
// something the server does not serve. It is the contract the server and
// the interface both build against: a route's shapes are the Go types
// named in its Body, Reply and Events.

type object = map[string]any

var pathParam = regexp.MustCompile(`\{([a-z_]+)\}`)

// Document returns the OpenAPI document for routes, as indented JSON that
// is the same every time for the same routes.
func Document(routes []Route, version string) ([]byte, error) {
	if err := Check(routes); err != nil {
		return nil, err
	}
	paths := object{}
	for _, rt := range routes {
		op := object{"summary": rt.Summary}
		var params []object
		for _, m := range pathParam.FindAllStringSubmatch(rt.Path, -1) {
			params = append(params, object{"name": m[1], "in": "path", "required": true, "schema": object{"type": "string"}})
		}
		for _, q := range rt.Query {
			params = append(params, object{"name": q.Name, "in": "query", "description": q.Description, "schema": object{"type": "string"}})
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if rt.Body != nil {
			op["requestBody"] = object{"required": true, "content": object{"application/json": object{"schema": schemaOf(reflect.TypeOf(rt.Body), 0)}}}
		}
		ok := object{"description": "The call did what was asked."}
		switch {
		case len(rt.Events) > 0:
			// OpenAPI has no words for the events of a stream, so they are
			// listed under a name of Keystone's own.
			var events []object
			for _, e := range rt.Events {
				events = append(events, object{"event": e.Name, "description": e.Description, "data": schemaOf(reflect.TypeOf(e.Data), 0)})
			}
			ok["content"] = object{"text/event-stream": object{"schema": object{"type": "string"}, "x-keystone-events": events}}
		case rt.Reply != nil:
			ok["content"] = object{"application/json": object{"schema": schemaOf(reflect.TypeOf(rt.Reply), 0)}}
		}
		op["responses"] = object{
			"200":     ok,
			"default": object{"description": "The call was refused or failed.", "content": object{"application/json": object{"schema": object{"$ref": "#/components/schemas/Error"}}}},
		}
		if rt.Door {
			op["security"] = []object{}
			op["x-keystone-door"] = true
		}
		if rt.Admin { // P15-01: an admin account's route, with the admin cookie
			op["x-keystone-admin"] = true
			if !rt.Door {
				op["security"] = []object{{"admin": []string{}}}
			}
		}
		item, _ := paths[rt.Path].(object)
		if item == nil {
			item = object{}
			paths[rt.Path] = item
		}
		item[strings.ToLower(rt.Method)] = op
	}
	doc := object{
		"openapi": "3.1.0",
		"info": object{
			"title":       "Keystone",
			"version":     version,
			"description": "Every client of Keystone uses this API. A call carries its session in the keystone_session cookie, and the session alone says whose data the call reaches. A route marked x-keystone-door is served before sign-in. A route marked x-keystone-admin is an admin account's: it carries the admin session in the keystone_admin cookie, a person's session is refused there (403 not_admin), and an admin's session is refused on every other route (403 admin_account). Every answer carries an X-Request-Id header.",
		},
		"paths":    paths,
		"security": []object{{"session": []string{}}},
		"components": object{
			"securitySchemes": object{"session": object{"type": "apiKey", "in": "cookie", "name": CookieName}, "admin": object{"type": "apiKey", "in": "cookie", "name": AdminCookieName}},
			"schemas":         object{"Error": schemaOf(reflect.TypeOf(ErrorBody{}), 0)},
		},
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

var (
	timeType = reflect.TypeOf(time.Time{})
	rawType  = reflect.TypeOf(json.RawMessage{})
)

// schemaOf writes a Go type as a JSON schema, following the same json tags
// encoding/json reads. A type that holds itself stops after a few levels.
func schemaOf(t reflect.Type, depth int) object {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == timeType:
		return object{"type": "string", "format": "date-time"}
	case t == rawType || depth > 8:
		return object{}
	}
	switch t.Kind() {
	case reflect.String:
		return object{"type": "string"}
	case reflect.Bool:
		return object{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return object{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return object{"type": "number"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return object{"type": "string", "contentEncoding": "base64"}
		}
		return object{"type": "array", "items": schemaOf(t.Elem(), depth+1)}
	case reflect.Map:
		return object{"type": "object", "additionalProperties": schemaOf(t.Elem(), depth+1)}
	case reflect.Struct:
		props, required := object{}, []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			props[name] = schemaOf(f.Type, depth+1)
			if !strings.Contains(opts, "omitempty") && !strings.Contains(opts, "omitzero") {
				required = append(required, name)
			}
		}
		s := object{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	return object{}
}
