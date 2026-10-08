package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/collimport/bruno"
	"github.com/Veyal/interseptor/internal/collimport/har"
	"github.com/Veyal/interseptor/internal/collimport/insomnia"
	"github.com/Veyal/interseptor/internal/collimport/postman"
)

// The Insomnia, Bruno and HAR/Burp importers join Postman, OpenAPI and curl
// behind the same preview/commit routes. Every format is normalised to a
// postman.Result so preview and commit share one path, and every script
// arrives quarantined (the store merge carries no trust and no capabilities).

const importFormatsList = "auto, postman, openapi, curl, insomnia, bruno, bruno-files, har, burp"

func parseInsomniaImport(data []byte) (*postman.Result, int, error) {
	r, err := insomnia.Parse(data, insomnia.Options{})
	if err != nil {
		switch {
		case errors.Is(err, insomnia.ErrTooLarge):
			return nil, http.StatusRequestEntityTooLarge, err
		case errors.Is(err, insomnia.ErrNotInsomnia), errors.Is(err, insomnia.ErrNothingFound):
			return nil, http.StatusUnsupportedMediaType, err
		}
		return nil, http.StatusBadRequest, errors.New("could not parse the Insomnia export: " + err.Error())
	}
	return &postman.Result{Kind: postman.KindCollection, Collection: r.Collection, Items: r.Items, Variables: r.Variables,
		Environments: r.Environments, Report: r.Report, SecretValues: r.SecretValues}, 0, nil
}

func brunoResult(r *bruno.Result) *postman.Result {
	return &postman.Result{Kind: postman.KindCollection, Collection: r.Collection, Items: r.Items, Variables: r.Variables,
		Environments: r.Environments, Report: r.Report, SecretValues: r.SecretValues}
}

func brunoErr(err error) (int, error) {
	switch {
	case errors.Is(err, bruno.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, err
	case errors.Is(err, bruno.ErrNoBru), errors.Is(err, bruno.ErrNotBru):
		return http.StatusUnsupportedMediaType, err
	}
	return http.StatusBadRequest, errors.New("could not parse the Bruno file: " + err.Error())
}

func parseBrunoImport(data []byte) (*postman.Result, int, error) {
	r, err := bruno.Parse(data, bruno.Options{})
	if err != nil {
		code, e := brunoErr(err)
		return nil, code, e
	}
	return brunoResult(r), 0, nil
}

// parseBrunoFilesImport reads a Bruno collection folder sent as
// {files:[{path,text}]}: paths are logical (the package never touches disk).
func parseBrunoFilesImport(data []byte) (*postman.Result, int, error) {
	var in struct {
		Files []struct {
			Path string `json:"path"`
			Text string `json:"text"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &in); err != nil || len(in.Files) == 0 {
		return nil, http.StatusBadRequest, errors.New(`bruno-files expects {"files":[{"path":"...","text":"..."}]}`)
	}
	files := make([]bruno.File, 0, len(in.Files))
	for _, f := range in.Files {
		files = append(files, bruno.File{Path: f.Path, Data: []byte(f.Text)})
	}
	r, err := bruno.ParseFiles(files, bruno.Options{})
	if err != nil {
		code, e := brunoErr(err)
		return nil, code, e
	}
	return brunoResult(r), 0, nil
}

func harResult(r *har.Result) *postman.Result {
	return &postman.Result{Kind: postman.KindCollection, Collection: r.Collection, Items: r.Items, Report: r.Report}
}

func harErr(err error, what string) (int, error) {
	switch {
	case errors.Is(err, har.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, err
	case errors.Is(err, har.ErrEmpty):
		return http.StatusUnsupportedMediaType, err
	}
	return http.StatusBadRequest, errors.New("could not parse the " + what + " file: " + err.Error())
}

func parseHARImport(data []byte) (*postman.Result, int, error) {
	r, err := har.ParseHAR(data, har.Options{})
	if err != nil {
		code, e := harErr(err, "HAR")
		return nil, code, e
	}
	return harResult(r), 0, nil
}

func parseBurpImport(data []byte) (*postman.Result, int, error) {
	r, err := har.ParseBurp(bytes.NewReader(data), har.Options{})
	if err != nil {
		code, e := harErr(err, "Burp")
		return nil, code, e
	}
	return harResult(r), 0, nil
}

func looksLikeBru(data []byte) bool {
	t := strings.TrimLeft(string(data), " \t\r\n\ufeff")
	return strings.HasPrefix(t, "meta {") || strings.HasPrefix(t, "meta{")
}

func looksLikeXML(data []byte) bool {
	t := strings.TrimLeft(string(data), " \t\r\n\ufeff")
	return strings.HasPrefix(t, "<?xml") || strings.HasPrefix(t, "<items")
}

func looksLikeInsomniaYAML(data []byte) bool {
	head := string(data)
	if len(head) > 2048 {
		head = head[:2048]
	}
	return strings.Contains(head, "collection.insomnia.rest")
}

// sniffJSON picks the importer for a JSON document by its top-level keys; a
// document with no telltale key falls back to trying each importer in turn.
func sniffJSON(data []byte) (*postman.Result, int, error) {
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) == nil {
		switch {
		case top["log"] != nil:
			return parseHARImport(data)
		case top["_type"] != nil || top["resources"] != nil || top["__export_format"] != nil:
			return parseInsomniaImport(data)
		case top["openapi"] != nil || top["swagger"] != nil:
			return parseOpenAPIImport(data)
		}
	}
	res, code, err := parsePostmanImport(data)
	if err == nil || code != http.StatusUnsupportedMediaType || !errors.Is(err, postman.ErrNotPostman) {
		return res, code, err
	}
	for _, try := range []func([]byte) (*postman.Result, int, error){parseOpenAPIImport, parseInsomniaImport, parseHARImport} {
		if r2, _, e2 := try(data); e2 == nil {
			return r2, 0, nil
		}
	}
	return res, code, err
}
