package web

import (
	"net/http"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
)

// UserInfo basic info about a user from a session.
type UserInfo struct {
	LoggedIn   bool                   `json:"loggedIn"`
	Properties map[string]interface{} `json:"properties"`
}

// GetEmail the email.
func (u *UserInfo) GetEmail() string {
	if u.Properties == nil {
		return ""
	}
	s, ok := u.Properties["email"].(string)
	if !ok {
		return ""
	}
	return s
}

// GetEmailVerified if the email h as been verified.
func (u *UserInfo) GetEmailVerified() bool {
	if u.Properties == nil {
		return false
	}
	b, ok := u.Properties["email_verified"].(bool)
	if !ok {
		return false
	}
	return b
}

// GetBool gets an attribute as a bool.
func (u *UserInfo) GetBool(name string) bool {
	if u.Properties == nil {
		return false
	}

	v, found := u.Properties[name]
	if !found {
		return false
	}

	b, good := v.(bool)
	if !good {
		return false
	}

	return b
}

// GetUpdatedAt gets the timestamp indicating when the user's profile was last updated/modified.
func (u *UserInfo) GetUpdatedAt() string {
	if u.Properties == nil {
		return ""
	}

	updatedAt, ok := u.Properties["updated_at"].(string)
	if !ok {
		return ""
	}
	return updatedAt
}

// GetLoggedInUserInfo figures out if the session is associated with a user.
func GetLoggedInUserInfo(sessions *SessionManager, r *http.Request) (UserInfo, error) {
	ui := UserInfo{LoggedIn: false, Properties: map[string]interface{}{"email": ""}}

	session, err := sessions.Get(r, false)
	if err != nil {
		if errors.Is(err, errNoSession) {
			return ui, nil
		}
		return ui, err
	}

	v := session.Data["profile"]
	if v == nil {
		return ui, nil
	}

	ui.LoggedIn = true
	var ok bool
	ui.Properties, ok = v.(bson.M)
	if !ok {
		return ui, errors.Errorf("expected bson.M but got %T", v)
	}

	return ui, nil
}
