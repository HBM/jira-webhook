package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	jira "github.com/andygrunwald/go-jira"
	"github.com/buger/jsonparser"

	"github.com/rs/zerolog/log"
)

const (
	// StoryPoints is custom field identifier for story points.
	StoryPoints = "customfield_10021"

	// Epic is a custom field identifier for epic id.
	Epic = "customfield_10025"

	// IssueUpdated is event name for issue updated.
	IssueUpdated = "issue_updated"

	// IssueCreated is event name for issue created.
	IssueCreated = "issue_created"
)

// User is a JIRA user.
type User struct {
	Active       bool              `json:"active"`
	AvatarURLs   map[string]string `json:"avatarUrls"`
	DisplayName  string            `json:"displayName"`
	EmailAddress string            `json:"emailAddress"`
	Key          string            `json:"key"`
	Self         string            `json:"self"`
	Name         string            `json:"name"`
	TimeZone     string            `json:"timeZone"`
}

// Issue is a single JIRA issue.
type Issue struct {
	Fields Fields `json:"fields"`
	ID     string `json:"id"`
	Key    string `json:"key"`
	Self   string `json:"self"`
}

// Priority is a field priority.
type Priority struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Self    string `json:"self"`
	IconURL string `json:"iconUrl"`
}

// Worklog is a field type.
type Worklog struct {
	MaxResults float64     `json:"maxResults"`
	StartAt    float64     `json:"startAt"`
	Total      float64     `json:"total"`
	Worklogs   interface{} `json:"worklogs"`
}

// Watches is a field type.
type Watches struct {
	Self       string  `json:"self"`
	WatchCount float64 `json:"watchCount"`
	IsWatching bool    `json:"isWatching"`
}

// AggregateProgress is a field field.
type AggregateProgress struct {
	Progress float64 `json:"progress"`
	Total    float64 `json:"total"`
}

// FixVersion is a Fields field.
type FixVersion struct {
	Archived    bool   `json:"archived"`
	Description string `json:"description"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	ReleaseDate string `json:"releaseDate"`
	Released    bool   `json:"released"`
	Self        string `json:"self"`
}

// IssueType is a Fields field.
type IssueType struct {
	AvatarID    int    `json:"avatarId"`
	Description string `json:"description"`
	IconURL     string `json:"iconUrl"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Self        string `json:"self"`
	Subtask     bool   `json:"subtask"`
}

// Fields is an issue field.
type Fields struct {
	AggregateProgress AggregateProgress `json:"aggregateprogress"`
	Created           string            `json:"created"`
	Creator           User              `json:"creator"`
	Description       string            `json:"description"`
	Duedate           interface{}       `json:"duedate"`
	FixVersions       []FixVersion      `json:"fixVersions"`
	IssueType         IssueType         `json:"issueType"`
	Priority          Priority          `json:"priority"`
	Reporter          User              `json:"reporter"`
	Timespent         interface{}       `json:"timespent"`
	Timeestimate      interface{}       `json:"timeestimate"`
	Watches           Watches           `json:"watches"`
	Worklog           Worklog           `json:"worklog"`
	CustomFields      map[string]interface{}
}

// Event is the main wrapper for a JIRA webhook event.
type Event struct {
	Changelog          Changelog `json:"changelog"`
	Issue              Issue     `json:"issue"`
	IssueEventTypeName string    `json:"issue_event_type_name"`
	Timestamp          float64   `json:"timestamp"`
	User               User      `json:"user"`
	WebhookEvent       string    `json:"webhookEvent"`
}

// Changelog is an event changelog.
type Changelog struct {
	ID    string `json:"id"`
	Items []Item `json:"items"`
}

// Item is a changelog item.
type Item struct {
	Field      string `json:"field"`
	FieldType  string `json:"fieldType"`
	From       string `json:"from"`
	FromString string `json:"fromString"`
	To         string `json:"to"`
	ToString   string `json:"toString"`
}

func main() {
	// get info from docker run ... command
	username := os.Getenv("JIRA_USERNAME")
	password := os.Getenv("JIRA_PASSWORD")

	// create jira transport
	tp := jira.BasicAuthTransport{
		Username: username,
		Password: password,
	}

	// create jira client with authenticated user transport
	client, err := jira.NewClient(tp.Client(), "https://jirat.hbm.com/")
	if err != nil {
		panic(err)
	}

	// add http handler which receives the POST requests
	http.HandleFunc("/webhooks", func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}

		var event Event
		if err := json.Unmarshal(body, &event); err != nil {
			panic(err)
		}

		log.Info().
			Str("event", event.IssueEventTypeName).
			Str("description", event.Issue.Fields.Description)

		// handle customfield_
		event.Issue.Fields.CustomFields = make(map[string]interface{})
		jsonparser.ObjectEach(body, func(key []byte, value []byte, dataType jsonparser.ValueType, offset int) error {
			prefix := []byte("customfield_")
			k := string(key)
			if bytes.HasPrefix(key, prefix) {
				if dataType == jsonparser.Number {
					float, err := jsonparser.ParseFloat(value)
					if err != nil {
						panic(err)
					}
					event.Issue.Fields.CustomFields[k] = float
				} else if dataType == jsonparser.String {
					s, err := jsonparser.ParseString(value)
					if err != nil {
						panic(err)
					}
					event.Issue.Fields.CustomFields[k] = s
				}
			}
			return nil
		}, "issue", "fields")

		// handle events for newly created issues only
		if event.IssueEventTypeName == IssueCreated {

			// check if @bot is mentioned
			if strings.Contains(event.Issue.Fields.Description, "@bot subtract") {
				issueStoryPoints := event.Issue.Fields.CustomFields[StoryPoints].(float64)
				epicName := event.Issue.Fields.CustomFields[Epic].(string)

				// get epic from jira
				epic, _, err := client.Issue.Get(epicName, nil)
				if err != nil {
					panic(err)
				}
				epicStoryPoints := epic.Fields.Unknowns[StoryPoints].(float64)

				log.Info().
					Str("id", epic.ID).
					Str("key", epic.Key).
					Msg("epic found")

				// calculate new epic story points
				newEpicStoryPoints := epicStoryPoints - issueStoryPoints

				// create update request
				request := map[string]interface{}{
					"fields": map[string]interface{}{
						StoryPoints: newEpicStoryPoints,
					},
				}

				// update epic with new story points
				res, err := client.Issue.UpdateIssue(epicName, request)
				if err != nil {
					panic(err)
				}

				log.Info().
					Int("status code", res.StatusCode).
					Msg("epic updated")

				// create comment for newly created issue
				comment := &jira.Comment{
					Body: fmt.Sprintf(
						"subtracted %d story points from epic %s. the epic has %d story points left.",
						int(issueStoryPoints),
						epicName,
						int(newEpicStoryPoints),
					),
				}

				// add comment to newly created issue
				c, _, err := client.Issue.AddComment(event.Issue.Key, comment)
				if err != nil {
					panic(err)
				}

				log.Info().
					Str("id", c.ID).
					Str("self", c.Self).
					Msg("comment added")
			}

		}

	})

	// show info message to user
	log.Info().Msg("server running on port 8060")

	// start server
	log.Fatal().Err(http.ListenAndServe(":8060", nil))
}
