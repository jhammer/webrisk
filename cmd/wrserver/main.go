// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// Command wrserver is an application for serving URL lookups via a simple API.
//
// In order to abstract away the complexities of the Web Risk API v4, the
// wrserver application can be used to serve a subset of API v4 over HTTP.
// This subset is intentionally small so that it would be easy to implement by
// a client. It is intended for wrserver to either be running locally on a
// client's machine or within the same local network. That way it can handle
// most local API calls before resorting to making an API call to the actual
// Web Risk API over the internet.
//
// Usage of wrserver looks something like this:
//
//	             _________________
//	            |                 |
//	            |  Web Risk  |
//	            |  API v4 servers |
//	            |_________________|
//	                     |
//	            ~ ~ ~ ~ ~ ~ ~ ~ ~ ~
//	               The Internet
//	            ~ ~ ~ ~ ~ ~ ~ ~ ~ ~
//	                     |
//	              _______V_______
//	             |               |
//	             |   SBServer    |
//	      +------|  Application  |------+
//	      |      |_______________|      |
//	      |              |              |
//	 _____V_____    _____V_____    _____V_____
//	|           |  |           |  |           |
//	|  Client1  |  |  Client2  |  |  Client3  |
//	|___________|  |___________|  |___________|
//
// In theory, each client could directly use the Go WebriskClient implementation,
// but there are situations where that is not desirable. For example, the client
// may not be using the language Go, or there may be multiple clients in the
// same machine or local network that would like to share a local database
// and cache. The wrserver was designed to address these issues.
//
// The wrserver application is technically a proxy since it is itself actually
// an API v4 client. It connects the Web Risk API servers using an API key
// and maintains a local database and cache. However, it is also a server since
// it re-serves a subset of the API v4 endpoints. These endpoints are minimal
// in that they do not require each client to maintain state between calls.
//
// The assumption is that communication between SBServer and Client1, Client2,
// and Client3 is inexpensive, since they are within the same machine or same
// local network. Thus, the wrserver can efficiently satisfy some requests
// without talking to the global Web Risk servers since it has a
// potentially larger cache. Furthermore, it can multiplex multiple requests to
// the Web Risk servers on fewer TCP connections, reducing the cost for
// comparatively more expensive internet transfers.
//
// By default, the wrserver listens on localhost:8080 and serves the following
// API endpoints:
//
//	/v4/threatMatches:find
//	/v4/threatLists
//	/status
//	/r
//
// Endpoint: /v4/threatMatches:find
//
// This is a lightweight implementation of the API v4 threatMatches endpoint.
// Essentially, it takes in a list of URLs, and returns a list of threat matches
// for those URLs. Unlike the Web Risk API, it does not require an API key.
//
// Example usage:
//
//	# Send request to server:
//	$ curl \
//	  -H "Content-Type: application/json" \
//	  -X POST -d '{
//	      "threatInfo": {
//	          "threatTypes":      ["UNWANTED_SOFTWARE", "MALWARE"],
//	          "platformTypes":    ["ANY_PLATFORM"],
//	          "threatEntryTypes": ["URL"],
//	          "threatEntries": [
//	              {"url": "google.com"},
//	              {"url": "bad1url.org"},
//	              {"url": "bad2url.org"}
//	          ]
//	      }
//	  }' \
//	  localhost:8080/v4/threatMatches:find
//
//	# Receive response from server:
//	{
//	    "matches": [{
//	        "threat":          {"url": "bad1url.org"},
//	        "platformType":    "ANY_PLATFORM",
//	        "threatType":      "UNWANTED_SOFTWARE",
//	        "threatEntryType": "URL"
//	    }, {
//	        "threat":          {"url": "bad2url.org"},
//	        "platformType":    "ANY_PLATFORM",
//	        "threatType":      "UNWANTED_SOFTWARE",
//	        "threatEntryType": "URL"
//	    }, {
//	        "threat":          {"url": "bad2url.org"},
//	        "platformType":    "ANY_PLATFORM",
//	        "threatType":      "MALWARE",
//	        "threatEntryType": "URL"
//	    }]
//	}
//
// Endpoint: /v4/threatLists
//
// The endpoint returns a list of the threat lists that the wrserver is
// currently subscribed to. The threats returned by the earlier threatMatches
// API call may only be one of these types.
//
// Example usage:
//
//	# Send request to server:
//	$ curl -X GET localhost:8080/v4/threatLists
//
//	# Receive response from server:
//	{
//	    "threatLists": [{
//	        "threatType":      "MALWARE"
//	        "platformType":    "ANY_PLATFORM",
//	        "threatEntryType": "URL",
//	    }, {
//	        "threatType":      "SOCIAL_ENGINEERING",
//	        "platformType":    "ANY_PLATFORM"
//	        "threatEntryType": "URL",
//	    }, {
//	        "threatType":      "UNWANTED_SOFTWARE"
//	        "platformType":    "ANY_PLATFORM",
//	        "threatEntryType": "URL",
//	    }]
//	}
//
// Endpoint: /status
//
// The status endpoint allows a client to obtain some statistical information
// regarding the health of wrserver. It can be used to determine how many
// requests were satisfied locally by wrserver alone and how many requests
// were forwarded to the Web Risk API servers.
//
// Example usage:
//
//	$ curl localhost:8080/status
//	{
//	    "Stats" : {
//	        "QueriesByDatabase" : 132,
//	        "QueriesByCache" : 31,
//	        "QueriesByAPI" : 6,
//	        "QueriesFail" : 0,
//	    },
//	    "Error" : ""
//	}
//
// Endpoint: /r
//
// The redirector endpoint allows a client to pass in a query URL.
// If the URL is safe, the client is automatically redirected to the target.
// If the URL is unsafe, then an interstitial warning page is shown instead.
//
// Example usage:
//
//	$ curl -i localhost:8080/r?url=http://google.com
//	HTTP/1.1 302 Found
//	Location: http://google.com
//
//	$ curl -i localhost:8080/r?url=http://bad1url.org
//	HTTP/1.1 200 OK
//	Date: Wed, 13 Apr 2016 21:29:33 GMT
//	Content-Length: 1783
//	Content-Type: text/html; charset=utf-8
//
//	<!-- Warning interstitial page shown -->
//	...
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/webrisk"
	_ "github.com/google/webrisk/cmd/wrserver/statik"
	pb "github.com/google/webrisk/internal/webrisk_proto"
	"github.com/rakyll/statik/fs"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	statusPath     = "/status"
	findThreatPath = "/v1/uris:search"
	redirectPath   = "/r"
)

const (
	mimeJSON  = "application/json"
	mimeProto = "application/x-protobuf"
)

var (
	apiKeyFlag        = flag.String("apikey", os.Getenv("APIKEY"), "specify your Web Risk API key")
	srvAddrFlag       = flag.String("srvaddr", "0.0.0.0:8080", "TCP network address the HTTP server should use")
	proxyFlag         = flag.String("proxy", "", "proxy to use to connect to the HTTP server")
	databaseFlag      = flag.String("db", "", "path to the Web Risk database.")
	threatTypesFlag   = flag.String("threatTypes", "ALL", "threat types to check against")
	pminTTLFlag       = flag.String("pminTTL", os.Getenv("PMINTTL"), "minimum time to cache positive responses")
	nminTTLFlag       = flag.String("nminTTL", os.Getenv("NMINTTL"), "minimum time to cache negative responses")
	logAPIQueriesFlag = flag.Bool("logAPIQueries", os.Getenv("LOGAPIQUERIES") == "yes", "log queries by API")
)

var threatTemplate = map[webrisk.ThreatType]string{
	webrisk.ThreatTypeMalware:                   "/malware.tmpl",
	webrisk.ThreatTypeUnwantedSoftware:          "/unwanted.tmpl",
	webrisk.ThreatTypeSocialEngineering:         "/social_engineering.tmpl",
	webrisk.ThreatTypeSocialEngineeringExtended: "/social_engineering.tmpl",
}

const usage = `wrserver: starts a Web Risk API proxy server.

In order to abstract away the complexities of the Web Risk API v4, the
wrserver application can be used to serve a subset of the v4 API.
This subset is intentionally small so that it would be easy to implement by
a client. It is intended for wrserver to either be running locally on a
client's machine or within the same local network so that it can handle most
local API calls before resorting to making an API call to the actual
Web Risk API over the internet.

Usage: %s -apikey=$APIKEY

`

// unmarshal reads pbResp from req. The mime will either be JSON or ProtoBuf.
func unmarshal(req *http.Request, pbReq proto.Message) (string, error) {
	var mime string
	alt := req.URL.Query().Get("alt")
	if alt == "" {
		alt = req.Header.Get("Content-Type")
	}
	switch alt {
	case "json", mimeJSON:
		mime = mimeJSON
	case "proto", mimeProto:
		mime = mimeProto
	default:
		return mime, errors.New("invalid interchange format")
	}

	switch req.Header.Get("Content-Type") {
	case mimeJSON:
		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			return mime, err
		}
		if err := protojson.Unmarshal(body, pbReq); err != nil {
			return mime, err
		}
	case mimeProto:
		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			return mime, err
		}
		if err := proto.Unmarshal(body, pbReq); err != nil {
			return mime, err
		}
	}
	return mime, nil
}

// marshal writes pbResp into resp. The mime can either be JSON or ProtoBuf.
func marshal(resp http.ResponseWriter, pbResp proto.Message, mime string) error {
	resp.Header().Set("Content-Type", mime)
	switch mime {
	case mimeProto:
		body, err := proto.Marshal(pbResp)
		if err != nil {
			return err
		}
		if _, err := resp.Write(body); err != nil {
			return err
		}
	case mimeJSON:
		b, err := protojson.Marshal(pbResp)
		if err != nil {
			return err
		}
		if _, err := resp.Write(b); err != nil {
			return err
		}
	default:
		return errors.New("invalid interchange format")
	}
	return nil
}

// serveStatus writes a simple JSON with server status information to resp.
func serveStatus(resp http.ResponseWriter, req *http.Request, sb *webrisk.UpdateClient) {
	stats, sbErr := sb.Status()
	errStr := ""
	if sbErr != nil {
		errStr = sbErr.Error()
	}
	buf, err := json.Marshal(struct {
		Stats webrisk.Stats
		Error string
	}{stats, errStr})
	if err != nil {
		http.Error(resp, err.Error(), http.StatusInternalServerError)
		return
	}
	resp.Header().Set("Content-Type", mimeJSON)
	resp.Write(buf)
}

// serveLookups is a light-weight implementation of the "/v4/threatMatches:find"
// API endpoint. This allows clients to look up whether a given URL is safe.
// Unlike the official API, it does not require an API key.
// It supports both JSON and ProtoBuf.
func serveLookups(resp http.ResponseWriter, req *http.Request, sb *webrisk.UpdateClient) {
	if req.Method != "POST" {
		http.Error(resp, "invalid method", http.StatusBadRequest)
		return
	}

	// Decode the request message.
	pbReq := new(pb.SearchUrisRequest)
	mime, err := unmarshal(req, pbReq)
	if err != nil {
		http.Error(resp, err.Error(), http.StatusBadRequest)
		return
	}

	// TODO: Should this handler use the information in threatTypes,
	// platformTypes, and threatEntryTypes?

	// Parse the request message.
	urls := []string{pbReq.Uri}

	// Lookup the URL.
	utss, err := sb.LookupURLsContext(req.Context(), urls)
	if err != nil {
		http.Error(resp, err.Error(), http.StatusInternalServerError)
		return
	}

	// Compose the response message.
	pbResp := &pb.SearchUrisResponse{
		Threat: &pb.SearchUrisResponse_ThreatUri{},
	}
	for _, uts := range utss {
		// Use map to condense duplicate ThreatDescriptor entries.
		tdm := make(map[webrisk.ThreatType]bool)
		for _, ut := range uts {
			tdm[ut.ThreatType] = true
		}

		for td := range tdm {
			pbResp.Threat.ThreatTypes = append(pbResp.Threat.ThreatTypes, pb.ThreatType(td))
		}
	}

	// Encode the response message.
	if err := marshal(resp, pbResp, mime); err != nil {
		http.Error(resp, err.Error(), http.StatusInternalServerError)
		return
	}
}

func parseTemplates(fs http.FileSystem, t *template.Template, paths ...string) (*template.Template, error) {
	for _, path := range paths {
		file, err := fs.Open(path)
		if err != nil {
			return nil, err
		}
		tmpl, err := ioutil.ReadAll(file)
		if err != nil {
			return nil, err
		}
		t, err = t.Parse(string(tmpl))
		if err != nil {
			return nil, err
		}
	}
	return t, nil
}

// serveRedirector implements a basic HTTP redirector that will filter out
// redirect URLs that are unsafe according to the Web Risk API.
func serveRedirector(resp http.ResponseWriter, req *http.Request, sb *webrisk.UpdateClient, fs http.FileSystem) {
	rawURL := req.URL.Query().Get("url")
	if rawURL == "" || req.URL.Path != "/r" {
		http.NotFound(resp, req)
		return
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		http.Error(resp, err.Error(), http.StatusInternalServerError)
		return
	}
	threats, err := sb.LookupURLsContext(req.Context(), []string{rawURL})
	if err != nil {
		http.Error(resp, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(threats[0]) == 0 {
		http.Redirect(resp, req, rawURL, http.StatusFound)
		return
	}

	t := template.New("Web Risk Interstitial")
	for _, threat := range threats[0] {
		if tmpl, ok := threatTemplate[threat.ThreatType]; ok {
			t, err = parseTemplates(fs, t, tmpl, "/interstitial.html")
			if err != nil {
				http.Error(resp, err.Error(), http.StatusInternalServerError)
				return
			}
			err = t.Execute(resp, map[string]any{
				"Threat": threat,
				"Url":    parsedURL})
			if err != nil {
				http.Error(resp, err.Error(), http.StatusInternalServerError)
			}
			return
		}
	}
}

// newServer sets up handlers and an http server for status, findThreatMatches,
// redirect endpoint, and content for the interstitial warning page.
func newServer(wr *webrisk.UpdateClient, fs http.FileSystem) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc(statusPath, func(w http.ResponseWriter, r *http.Request) {
		serveStatus(w, r, wr)
	})
	mux.HandleFunc(findThreatPath, func(w http.ResponseWriter, r *http.Request) {
		serveLookups(w, r, wr)
	})
	mux.HandleFunc(redirectPath, func(w http.ResponseWriter, r *http.Request) {
		serveRedirector(w, r, wr, fs)
	})
	mux.Handle("/public/", http.StripPrefix("/public/", http.FileServer(fs)))

	return &http.Server{
		Addr:    *srvAddrFlag,
		Handler: mux,
	}
}

// runServer sets up a listener for interrupts, starts the passed HTTP server, and shuts down
// gracefully on an interrupt signal. It returns an exit channel that can be used to trigger
// cleanup and a server down channel that notifies the caller when the server is finished shutting
// down.
func runServer(srv *http.Server) (chan os.Signal, <-chan struct{}) {
	// start listening for interrupts
	exit := make(chan os.Signal, 1)
	down := make(chan struct{})

	// runs shutdown and cleanup on an exit signal
	go func() {
		<-exit
		fmt.Fprintln(os.Stdout, "\nStarting server shutdown...")

		timeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		srv.SetKeepAlivesEnabled(false)

		if err := srv.Shutdown(timeout); err != nil {
			log.Fatalf("Server error when shutting down: %s", err)
		}
		fmt.Fprintln(os.Stdout, "Server shutdown completed.")
	}()

	// runs our server until an exit signal is received
	go func() {
		fmt.Fprintln(os.Stdout, "Starting server at", srv.Addr)
		// this blocks our main thread until an interrupt signal
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %s", err)
		}
		close(down)
	}()

	return exit, down
}

func validateDuration(value string) string {
	if len(value) == 0 {
		return "0s"
	}

	return value
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, usage, os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *apiKeyFlag == "" {
		fmt.Fprintln(os.Stderr, "No -apikey specified")
		os.Exit(1)
	}
	pminTTL, err := time.ParseDuration(validateDuration(*pminTTLFlag))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Invalid -pminTTL")
		os.Exit(1)
	}
	nminTTL, err := time.ParseDuration(validateDuration(*nminTTLFlag))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Invalid -nminTTL")
		os.Exit(1)
	}
	conf := webrisk.Config{
		APIKey:                *apiKeyFlag,
		ProxyURL:              *proxyFlag,
		DBPath:                *databaseFlag,
		ThreatListArg:         *threatTypesFlag,
		Logger:                os.Stderr,
		PMinTTL:               pminTTL,
		NMinTTL:               nminTTL,
		ShouldLogQueriesByAPI: *logAPIQueriesFlag,
	}
	wr, err := webrisk.NewUpdateClient(conf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Unable to initialize Web Risk client: ", err)
		os.Exit(1)
	}
	statikFS, err := fs.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Unable to initialize static files: ", err)
		os.Exit(1)
	}

	srv := newServer(wr, statikFS)
	exit, down := runServer(srv)
	signal.Notify(exit, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	<-down
	fmt.Fprintln(os.Stdout, "wrserver exiting.")
}
