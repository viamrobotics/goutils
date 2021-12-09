package web

import (
	"log"
	"net/http"

	"go.mongodb.org/mongo-driver/bson"
)

// UserInfo basic info about a user from a session
type UserInfo struct {
	LoggedIn   bool
	Properties map[string]interface{}
}

// GetEmail the email
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

// GetEmailVerified if the email h as been verified
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

// GetBool gets an attribute as a bool
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

// GetLoggedInUserInfo figures out if the session is associated with a user
func GetLoggedInUserInfo(sessions *SessionManager, r *http.Request) (UserInfo, error) {
	ui := UserInfo{LoggedIn: false, Properties: map[string]interface{}{"email": ""}}

	session, err := sessions.Get(r, false)
	if err != nil {
		return ui, err
	}

	if session == nil {
		return ui, nil
	}

	v := session.Data["profile"]
	if v == nil {
		return ui, err
	}

	ui.LoggedIn = true
	ui.Properties = v.(bson.M)

	if ui.Properties == nil {
		log.Printf("how is properties nil for %v", r)
	}

	return ui, nil
}
