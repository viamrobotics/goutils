package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/edaniels/golog"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.opencensus.io/trace"

	mongoutils "go.viam.com/utils/mongo"
)

var webSessionsIndex = []mongo.IndexModel{
	{
		Keys: bson.D{
			{Key: "lastUpdated", Value: 1},
		},
		Options: options.Index().SetExpireAfterSeconds(30 * 24 * 3600),
	},
}

// SessionManager handles working with sessions from http.
type SessionManager struct {
	store      Store
	cookieName string
	logger     golog.Logger
}

// Session representation of a session.
type Session struct {
	store   Store
	manager *SessionManager

	isNew bool

	id   string
	Data bson.M
}

// Store actually stores raw data somewhere.
type Store interface {
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*Session, error)
	Save(ctx context.Context, s *Session) error
	SetSessionManager(*SessionManager)
}

// ----

// NewSessionManager creates a new SessionManager.
func NewSessionManager(theStore Store, logger golog.Logger) *SessionManager {
	sm := &SessionManager{store: theStore, cookieName: "session-id", logger: logger}
	theStore.SetSessionManager(sm)
	return sm
}

// Get get a session from the request via cookies.
func (sm *SessionManager) Get(r *http.Request, createIfNotExist bool) (*Session, error) {
	var s *Session
	id := ""

	c, err := r.Cookie(sm.cookieName)
	if !errors.Is(err, http.ErrNoCookie) {
		if c == nil {
			panic("wtf")
		}
		if err != nil {
			panic("wtf")
		}
		id = c.Value

		s, err = sm.store.Get(r.Context(), id)
		if err != nil && !errors.Is(err, errNoSession) {
			return nil, fmt.Errorf("couldn't get cookie from store: %w", err)
		}

		if s != nil {
			return s, nil
		}
	}

	if !createIfNotExist {
		return nil, errNoSession
	}

	if id == "" {
		id, err = sm.newID()
		if err != nil {
			return nil, fmt.Errorf("couldn't create new id: %w", err)
		}
	}

	s = &Session{
		store:   sm.store,
		manager: sm,
		isNew:   true,
		id:      id,
		Data:    bson.M{},
	}

	return s, nil
}

// DeleteSession deletes a session.
func (sm *SessionManager) DeleteSession(ctx context.Context, r *http.Request, w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sm.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
	})

	c, err := r.Cookie(sm.cookieName)
	if err == nil {
		err = sm.store.Delete(ctx, c.Value)
		if err != nil {
			sm.logger.Errorw("cannot delete cookie", "error", err)
		}
	}
}

func (sm *SessionManager) newID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(b), nil
}

// ----

// Save saves a session back to the store it came freom.
func (s *Session) Save(ctx context.Context, r *http.Request, w http.ResponseWriter) error {
	if s.isNew {
		http.SetCookie(w, &http.Cookie{
			Name:     s.manager.cookieName,
			Value:    s.id,
			Path:     "/",
			MaxAge:   86400 * 7,
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteLaxMode,
			HttpOnly: true,
		})
		s.isNew = false
	}
	return s.store.Save(ctx, s)
}

// -----

// NewMongoDBSessionStore new MongoDB backed store.
func NewMongoDBSessionStore(ctx context.Context, coll *mongo.Collection) (Store, error) {
	if err := mongoutils.EnsureIndexes(ctx, coll, webSessionsIndex...); err != nil {
		return nil, errors.Wrap(err, "Failed to create indexes for webSessionsCollection")
	}

	return &mongoDBSessionStore{collection: coll}, nil
}

type mongoDBSessionStore struct {
	collection *mongo.Collection
	manager    *SessionManager
}

func (mss *mongoDBSessionStore) SetSessionManager(sm *SessionManager) {
	mss.manager = sm
}

func (mss *mongoDBSessionStore) Delete(ctx context.Context, id string) error {
	ctx, span := trace.StartSpan(ctx, "MongoDBSessionStore::Delete")
	defer span.End()

	_, err := mss.collection.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

var errNoSession = errors.New("no session found")

func (mss *mongoDBSessionStore) Get(ctx context.Context, id string) (*Session, error) {
	ctx, span := trace.StartSpan(ctx, "MongoDBSessionStore::Get")
	defer span.End()

	res := mss.collection.FindOne(ctx, bson.M{"_id": id})
	if res.Err() != nil {
		if errors.Is(res.Err(), mongo.ErrNoDocuments) {
			return nil, errNoSession
		}
		return nil, fmt.Errorf("couldn't load session from db: %w", res.Err())
	}

	m := bson.M{}
	if err := res.Decode(&m); err != nil {
		return nil, err
	}

	s := &Session{
		store:   mss,
		manager: mss.manager,
		isNew:   false,
		id:      id,
		Data:    m["data"].(bson.M),
	}

	return s, nil
}

func (mss *mongoDBSessionStore) Save(ctx context.Context, s *Session) error {
	ctx, span := trace.StartSpan(ctx, "MongoDBSessionStore::Save")
	defer span.End()

	doc := bson.M{
		"_id":        s.id,
		"lastUpdate": time.Now(),
		"data":       s.Data,
	}

	_, err := mss.collection.UpdateOne(ctx,
		bson.M{"_id": s.id},
		bson.M{"$set": doc},
		options.Update().SetUpsert(true),
	)

	return err
}

// ------

// NewMemorySessionStore creates a new memory session store.
func NewMemorySessionStore() Store {
	return &memorySessionStore{}
}

type memorySessionStore struct {
	data    map[string]*Session
	manager *SessionManager
}

func (mss *memorySessionStore) SetSessionManager(sm *SessionManager) {
	mss.manager = sm
}

func (mss *memorySessionStore) Delete(ctx context.Context, id string) error {
	if mss.data != nil {
		mss.data[id] = nil
	}
	return nil
}

func (mss *memorySessionStore) Get(ctx context.Context, id string) (*Session, error) {
	if mss.data == nil {
		return nil, errNoSession
	}
	return mss.data[id], nil
}

func (mss *memorySessionStore) Save(ctx context.Context, s *Session) error {
	if mss.data == nil {
		mss.data = map[string]*Session{}
	}
	mss.data[s.id] = s
	return nil
}
