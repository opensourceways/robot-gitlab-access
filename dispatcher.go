package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"

	"github.com/opensourceways/community-robot-lib/gitlabclient"
	"github.com/opensourceways/community-robot-lib/utils"
	"github.com/sirupsen/logrus"
	"github.com/xanzy/go-gitlab"
)

const (
	noteableTypeCommit       = "Commit"
	noteableTypeIssue        = "Issue"
	noteableTypeMergeRequest = "MergeRequest"
)

type noteEvent struct {
	ObjectKind       string `json:"object_kind"`
	ObjectAttributes struct {
		NoteableType string `json:"noteable_type"`
	} `json:"object_attributes"`
}

type dispatcher struct {
	agent *demuxConfigAgent

	userAgent string

	hc utils.HttpClient

	// Tracks running handlers for graceful shutdown
	wg sync.WaitGroup
}

func newDispatcher(agent *demuxConfigAgent, userAgent string) *dispatcher {
	return &dispatcher{
		agent:     agent,
		userAgent: userAgent,
		hc:        utils.HttpClient{MaxRetries: 3},
	}

}

func (d *dispatcher) wait() {
	d.wg.Wait() // Handle remaining requests
}

// ServeHTTP validates an incoming webhook and puts it into the event channel.
func (d *dispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventType, eventGUID, payload, ok := d.parseRequest(w, r)
	if !ok {
		return
	}

	fmt.Fprint(w, "Event received. Have a nice day.")

	l := logrus.WithFields(
		logrus.Fields{
			"event-type": eventType,
			"event-id":   eventGUID,
		},
	)

	if err := d.dispatch(eventType, payload, r.Header, l); err != nil {
		l.Error(err)
	}
}

func (d *dispatcher) parseRequest(w http.ResponseWriter, r *http.Request) (
	eventType string,
	uuid string,
	payload []byte,
	ok bool,
) {
	defer r.Body.Close()

	resp := func(code int, msg string) {
		http.Error(w, msg, code)
	}

	if r.Header.Get("User-Agent") != d.userAgent {
		resp(http.StatusBadRequest, "400 Bad Request: unknown User-Agent Header")
		return
	}

	if eventType = r.Header.Get("X-Gitlab-Event"); eventType == "" {
		resp(http.StatusBadRequest, "400 Bad Request: Missing X-Gitlab-Event Header")
		return
	}

	if uuid = r.Header.Get("X-Gitlab-Event-UUID"); uuid == "" {
		resp(http.StatusBadRequest, "400 Bad Request: Missing X-Gitlab-Event-UUID Header")
		return
	}

	v, err := ioutil.ReadAll(r.Body)
	if err != nil {
		resp(http.StatusInternalServerError, "500 Internal Server Error: Failed to read request body")
		return
	}

	payload = v
	ok = true

	return
}

func (d *dispatcher) dispatch(eventType string, payload []byte, h http.Header, l *logrus.Entry) error {
	org := ""
	repo := ""

	et := gitlab.EventType(eventType)
	switch et {
	case gitlab.EventTypeMergeRequest:
		e, err := gitlabclient.ConvertToMergeEvent(payload)
		if err != nil {
			return err
		}

		org, repo = gitlabclient.GetOrgRepo(e.Project.PathWithNamespace)

	case gitlab.EventTypeIssue:
		e, err := gitlabclient.ConvertToIssueEvent(payload)
		if err != nil {
			return err
		}

		org, repo = gitlabclient.GetOrgRepo(e.Project.PathWithNamespace)

	case gitlab.EventTypeNote:
		note := &noteEvent{}
		err := json.Unmarshal(payload, note)
		if err != nil {
			return err
		}

		if note.ObjectKind != string(gitlab.NoteEventTargetType) {
			return nil
		}

		switch note.ObjectAttributes.NoteableType {
		case noteableTypeCommit:
			e, err := gitlabclient.ConvertToCommitCommentEvent(payload)
			if err != nil {
				return err
			}
			org, repo = gitlabclient.GetOrgRepo(e.Project.PathWithNamespace)
		case noteableTypeMergeRequest:
			e, err := gitlabclient.ConvertToMergeCommentEvent(payload)
			if err != nil {
				return err
			}

			org, repo = gitlabclient.GetOrgRepo(e.Project.PathWithNamespace)
		case noteableTypeIssue:
			e, err := gitlabclient.ConvertToIssueCommentEvent(payload)
			if err != nil {
				return err
			}

			org, repo = gitlabclient.GetOrgRepo(e.Project.PathWithNamespace)

		default:
			return fmt.Errorf("unexpected noteable type %s", note.ObjectAttributes.NoteableType)
		}

	case gitlab.EventTypePush:
		e, err := gitlabclient.ConvertToPushEvent(payload)
		if err != nil {
			return err
		}

		org, repo = gitlabclient.GetOrgRepo(e.Project.PathWithNamespace)

	default:
		l.Debug("Ignoring unknown event type")
		return fmt.Errorf("unexpected event type: %s", eventType)
	}

	l = l.WithFields(logrus.Fields{
		"org":  org,
		"repo": repo,
	})

	endpoints := d.agent.getEndpoints(eventType)
	l.WithField("endpoints", strings.Join(endpoints, ", ")).Info("start dispatching event.")

	d.doDispatch(endpoints, payload, h, l)
	return nil
}

func (d *dispatcher) doDispatch(endpoints []string, payload []byte, h http.Header, l *logrus.Entry) {
	h.Set("User-Agent", "Robot-Gitlab-Access")

	newReq := func(endpoint string) (*http.Request, error) {
		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBuffer(payload))
		if err != nil {
			return nil, err
		}

		req.Header = h

		return req, nil
	}

	reqs := make(map[string]*http.Request, len(endpoints))

	for _, endpoint := range endpoints {
		if req, err := newReq(endpoint); err == nil {
			reqs[endpoint] = req
		} else {
			l.Errorf(
				"Error generating http request for endpoint:%s, err:%s",
				endpoint, err.Error(),
			)
		}
	}

	for endpoint, req := range reqs {
		d.wg.Add(1)

		// concurrent action is sending request not generating it.
		// so, generates requests first.
		go func(e string, req *http.Request) {
			defer d.wg.Done()

			if err := d.hc.ForwardTo(req, nil); err != nil {
				l.Errorf(
					"Error forwarding event to endpoint:%s, err:%s",
					e, err.Error(),
				)
			}
		}(endpoint, req)
	}
}
