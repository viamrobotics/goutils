package web

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/edaniels/golog"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestSession1(t *testing.T) {
	ctx := context.Background()
	sm := NewSessionManager(&memorySessionStore{}, golog.NewTestLogger(t))

	r, err := http.NewRequest(http.MethodGet, "http://localhost/", nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := sm.Get(r, false); !errors.Is(err, errNoSession) {
		t.Fatal(err)
	}

	s, err := sm.Get(r, true)
	if err != nil {
		t.Fatal(err)
	}

	if s == nil {
		t.Fatal("wtf")
	}

	w := &DummyWriter{}

	s.Data["a"] = 1
	s.Data["access_token"] = "the_access_token"
	s.Save(context.TODO(), r, w)

	if hasSession := sm.HasSessionWithAccessToken(ctx, "the_wrong_access_token"); hasSession {
		t.Fatal("should not have session with token")
	}

	if hasSession := sm.HasSessionWithAccessToken(ctx, "the_access_token"); !hasSession {
		t.Fatal("should have session with token")
	}
}

// ----

func TestMongoStore(t *testing.T) {
	connectCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	client, err := mongo.Connect(connectCtx)
	if err != nil {
		t.Skip()
		return
	}

	err = client.Ping(connectCtx, nil)
	if err != nil {
		t.Skip()
		return
	}

	ctx := context.Background()
	coll := client.Database("test").Collection("sessiontest1")
	store := &mongoDBSessionStore{coll, nil}
	defer coll.Drop(ctx)

	s1 := &Session{}
	s1.id = "foo"
	s1.Data = bson.M{"a": 1, "b": 2, "access_token": "testToken"}
	err = store.Save(ctx, s1)
	if err != nil {
		t.Fatal(err)
	}

	s2, err := store.Get(ctx, s1.id)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Data["a"].(int32) != 1 {
		t.Fatalf("a wrong: %s", s2.Data["a"])
	}
	if s2.Data["b"].(int32) != 2 {
		t.Fatal("b wrong")
	}

	if _, err := store.Get(ctx, "something"); !errors.Is(err, errNoSession) {
		t.Fatal(err)
	}

	hasSession, err := store.HasSessionWithToken(ctx, "no_token")
	if err != nil {
		t.Fatal(err)
	}
	if hasSession {
		t.Fatal("should not have session")
	}

	hasSession, err = store.HasSessionWithToken(ctx, "testToken")
	if err != nil {
		t.Fatal(err)
	}
	if !hasSession {
		t.Fatal("should have session")
	}
}

// ----

type DummyWriter struct {
	h http.Header
}

func (dw *DummyWriter) Header() http.Header {
	if dw.h == nil {
		dw.h = http.Header{}
	}
	return dw.h
}

func (dw *DummyWriter) Write(b []byte) (int, error) {
	return len(b), nil
}

func (dw *DummyWriter) WriteHeader(code int) {
}
