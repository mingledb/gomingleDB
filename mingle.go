// Package gomingleDB is a lightweight file-based NoSQL database (Go port of mingleDB).
// It uses BSON serialization with zlib compression and supports schema validation,
// query filters ($gt, $gte, $in, $regex, etc.), and basic authentication.
package gomingleDB

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/bson"
)

const (
	header         = "MINGLEDBv1"
	extension      = ".mgdb"
	defaultDBFile  = "database.mgdb"
	authCollection = "_auth"
)

var (
	ErrUsernameExists  = errors.New("username already exists")
	ErrAuthFailed      = errors.New("authentication failed")
	ErrValidation      = errors.New("validation error")
	ErrRequired        = errors.New("field is required")
	ErrTypeMismatch    = errors.New("field type mismatch")
	ErrUniqueViolation = errors.New("duplicate value for unique field")
)

// MingleDB is the main database handle.
type MingleDB struct {
	mu       sync.RWMutex
	dbPath   string
	schemas  map[string]SchemaDefinition
	sessions map[string]struct{} // authenticated usernames
}

// SchemaRule defines a single field rule (type, required, unique).
type SchemaRule struct {
	Type     string // "string", "number"
	Required bool
	Unique   bool
}

// SchemaDefinition is a map of field name -> rule.
type SchemaDefinition map[string]SchemaRule

// New creates a MingleDB instance backed by a single .mgdb file.
// If dbPath is a directory, "database.mgdb" is created inside it.
func New(dbPath string) *MingleDB {
	resolved := resolveDBPath(dbPath)
	_ = os.MkdirAll(filepath.Dir(resolved), 0755)
	return &MingleDB{
		dbPath:   resolved,
		schemas:  make(map[string]SchemaDefinition),
		sessions: make(map[string]struct{}),
	}
}

func resolveDBPath(dbPath string) string {
	if strings.TrimSpace(dbPath) == "" {
		return defaultDBFile
	}
	if strings.HasSuffix(strings.ToLower(dbPath), extension) {
		return dbPath
	}
	return filepath.Join(dbPath, defaultDBFile)
}

// Reset wipes the database file and clears schemas and auth state.
func (db *MingleDB) Reset() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := os.Remove(db.dbPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	db.schemas = make(map[string]SchemaDefinition)
	db.sessions = make(map[string]struct{})
	return nil
}

// DefineSchema sets the schema for a collection.
func (db *MingleDB) DefineSchema(collection string, schema SchemaDefinition) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.schemas == nil {
		db.schemas = make(map[string]SchemaDefinition)
	}
	db.schemas[collection] = schema
}

// ListCollections returns the names of all collections in the single DB file and schemas.
func (db *MingleDB) ListCollections() ([]string, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	records, err := db.readAllRecordsLocked()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, r := range records {
		seen[r.Collection] = struct{}{}
	}
	for name := range db.schemas {
		seen[name] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names, nil
}

// DBDir returns the current database file path.
func (db *MingleDB) DBDir() string {
	return db.dbPath
}

// GetSchema returns the schema for a collection if defined, and whether it exists.
func (db *MingleDB) GetSchema(collection string) (SchemaDefinition, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	s, ok := db.schemas[collection]
	return s, ok
}

func (db *MingleDB) ensureDatabaseFileLocked() error {
	if _, err := os.Stat(db.dbPath); err == nil {
		return nil
	}
	meta, _ := json.Marshal(map[string]string{"scope": "database", "format": "single-file-v2"})
	var metaLen [4]byte
	binary.LittleEndian.PutUint32(metaLen[:], uint32(len(meta)))
	return os.WriteFile(db.dbPath, append(append([]byte(header), metaLen[:]...), meta...), 0644)
}

func (db *MingleDB) hashPassword(password string) string {
	h := sha256.Sum256([]byte(password))
	return hex.EncodeToString(h[:])
}

// RegisterUser adds a user to the _auth collection. Returns ErrUsernameExists if username exists.
func (db *MingleDB) RegisterUser(username, password string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if err := db.ensureDatabaseFileLocked(); err != nil {
		return err
	}
	users, err := db.findAllLocked(authCollection)
	if err != nil {
		return err
	}
	for _, u := range users {
		if u["username"] == username {
			return ErrUsernameExists
		}
	}
	hashed := db.hashPassword(password)
	return db.insertOneLocked(authCollection, map[string]interface{}{
		"username": username,
		"password": hashed,
	})
}

// Login authenticates a user and marks them as authenticated. Returns ErrAuthFailed on failure.
func (db *MingleDB) Login(username, password string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	users, err := db.findAllLocked(authCollection)
	if err != nil {
		return err
	}
	var user map[string]interface{}
	for _, u := range users {
		if u["username"] == username {
			user = u
			break
		}
	}
	if user == nil || user["password"] != db.hashPassword(password) {
		return ErrAuthFailed
	}
	if db.sessions == nil {
		db.sessions = make(map[string]struct{})
	}
	db.sessions[username] = struct{}{}
	return nil
}

// IsAuthenticated reports whether the username has an active session.
func (db *MingleDB) IsAuthenticated(username string) bool {
	db.mu.RLock()
	defer db.mu.RUnlock()
	_, ok := db.sessions[username]
	return ok
}

// Logout removes the user from the session set.
func (db *MingleDB) Logout(username string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	delete(db.sessions, username)
}

// InsertOne appends one document to the collection. Schema is validated if defined.
func (db *MingleDB) InsertOne(collection string, doc map[string]interface{}) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if err := db.ensureDatabaseFileLocked(); err != nil {
		return err
	}
	if err := db.validateSchemaLocked(collection, doc); err != nil {
		return err
	}
	return db.insertOneLocked(collection, doc)
}

// FindAll returns all documents in the collection.
func (db *MingleDB) FindAll(collection string) ([]map[string]interface{}, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.findAllLocked(collection)
}

// Find returns documents that match the filter. Empty filter matches all.
func (db *MingleDB) Find(collection string, filter map[string]interface{}) ([]map[string]interface{}, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	docs, err := db.findAllLocked(collection)
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	for _, d := range docs {
		if matchQuery(d, filter) {
			out = append(out, d)
		}
	}
	return out, nil
}

// FindOne returns the first document matching the filter, or nil.
func (db *MingleDB) FindOne(collection string, filter map[string]interface{}) (map[string]interface{}, error) {
	docs, err := db.Find(collection, filter)
	if err != nil || len(docs) == 0 {
		return nil, err
	}
	return docs[0], nil
}

// UpdateOne updates the first document matching the query with the given update fields. Returns true if one was updated.
func (db *MingleDB) UpdateOne(collection string, query, update map[string]interface{}) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	docs, err := db.findAllLocked(collection)
	if err != nil {
		return false, err
	}
	var updated bool
	for i := range docs {
		if !updated && matchQuery(docs[i], query) {
			updated = true
			for k, v := range update {
				docs[i][k] = v
			}
		}
	}
	if updated {
		return true, db.rewriteCollectionLocked(collection, docs)
	}
	return false, nil
}

// DeleteOne removes the first document matching the query. Returns true if one was deleted.
func (db *MingleDB) DeleteOne(collection string, query map[string]interface{}) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	docs, err := db.findAllLocked(collection)
	if err != nil {
		return false, err
	}
	var out []map[string]interface{}
	var deleted bool
	for _, d := range docs {
		if !deleted && matchQuery(d, query) {
			deleted = true
			continue
		}
		out = append(out, d)
	}
	if deleted {
		return true, db.rewriteCollectionLocked(collection, out)
	}
	return false, nil
}

func (db *MingleDB) validateSchemaLocked(collection string, doc map[string]interface{}) error {
	schema, ok := db.schemas[collection]
	if !ok {
		return nil
	}
	all, _ := db.findAllLocked(collection)
	for key, rule := range schema {
		val, has := doc[key]
		if rule.Required && (val == nil || !has) {
			return fmt.Errorf("%w: field %q is required", ErrValidation, key)
		}
		if val != nil && has {
			switch rule.Type {
			case "string":
				if _, ok := val.(string); !ok {
					return fmt.Errorf("%w: field %q must be string", ErrValidation, key)
				}
			case "number":
				if _, ok := val.(float64); !ok {
					if _, ok := val.(int); !ok {
						if _, ok := val.(int64); !ok {
							return fmt.Errorf("%w: field %q must be number", ErrValidation, key)
						}
					}
				}
			}
			if rule.Unique {
				for _, d := range all {
					if valueEqual(d[key], val) {
						return fmt.Errorf("%w: duplicate value for unique field %q", ErrValidation, key)
					}
				}
			}
		}
	}
	return nil
}

type dbRecord struct {
	Collection string                 `bson:"collection" json:"collection"`
	Doc        map[string]interface{} `bson:"doc" json:"doc"`
}

func (db *MingleDB) insertOneLocked(collection string, doc map[string]interface{}) error {
	bsonBytes, err := marshalBSON(map[string]interface{}{
		"collection": collection,
		"doc":        doc,
	})
	if err != nil {
		return err
	}
	compressed := compress(bsonBytes)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(compressed)))
	f, err := os.OpenFile(db.dbPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(lenBuf[:], compressed...))
	f.Close()
	return err
}

func (db *MingleDB) findAllLocked(collection string) ([]map[string]interface{}, error) {
	records, err := db.readAllRecordsLocked()
	if err != nil {
		return nil, err
	}
	var docs []map[string]interface{}
	for _, r := range records {
		if r.Collection == collection {
			docs = append(docs, r.Doc)
		}
	}
	return docs, nil
}

func (db *MingleDB) rewriteCollectionLocked(collection string, docs []map[string]interface{}) error {
	records, err := db.readAllRecordsLocked()
	if err != nil {
		return err
	}
	out := make([]dbRecord, 0, len(records))
	for _, r := range records {
		if r.Collection != collection {
			out = append(out, r)
		}
	}
	for _, d := range docs {
		out = append(out, dbRecord{Collection: collection, Doc: d})
	}
	return db.writeAllRecordsLocked(out)
}

func (db *MingleDB) readAllRecordsLocked() ([]dbRecord, error) {
	data, err := os.ReadFile(db.dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) < len(header)+4 {
		return nil, nil
	}
	if string(data[:len(header)]) != header {
		return nil, errors.New("invalid mingleDB file header")
	}
	offset := len(header)
	metaLen := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if offset+metaLen > len(data) {
		return nil, nil
	}
	metaBytes := data[offset : offset+metaLen]
	offset += metaLen
	meta := map[string]interface{}{}
	_ = json.Unmarshal(metaBytes, &meta)
	legacyCollection, _ := meta["collection"].(string)

	var records []dbRecord
	for offset+4 <= len(data) {
		docLen := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		offset += 4
		if offset+docLen > len(data) {
			break
		}
		compressed := data[offset : offset+docLen]
		offset += docLen
		bsonBytes, err := decompress(compressed)
		if err != nil {
			return nil, err
		}
		doc, err := unmarshalBSON(bsonBytes)
		if err != nil {
			return nil, err
		}

		if c, ok := doc["collection"].(string); ok {
			if payload, ok := toStringMap(doc["doc"]); ok {
				records = append(records, dbRecord{Collection: c, Doc: payload})
				continue
			}
		}
		if legacyCollection != "" {
			records = append(records, dbRecord{Collection: legacyCollection, Doc: doc})
		}
	}
	return records, nil
}

func (db *MingleDB) writeAllRecordsLocked(records []dbRecord) error {
	meta, _ := json.Marshal(map[string]string{"scope": "database", "format": "single-file-v2"})
	var metaLen [4]byte
	binary.LittleEndian.PutUint32(metaLen[:], uint32(len(meta)))
	var body []byte
	for _, r := range records {
		bsonBytes, err := marshalBSON(map[string]interface{}{
			"collection": r.Collection,
			"doc":        r.Doc,
		})
		if err != nil {
			return err
		}
		compressed := compress(bsonBytes)
		var lenBuf [4]byte
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(compressed)))
		body = append(body, append(lenBuf[:], compressed...)...)
	}
	return os.WriteFile(db.dbPath, append(append([]byte(header), metaLen[:]...), append(meta, body...)...), 0644)
}

func compress(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.Bytes()
}

func decompress(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func marshalBSON(doc map[string]interface{}) ([]byte, error) {
	return bson.Marshal(bson.M(doc))
}

func unmarshalBSON(data []byte) (map[string]interface{}, error) {
	var out bson.M
	if err := bson.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return map[string]interface{}(out), nil
}

func toStringMap(v interface{}) (map[string]interface{}, bool) {
	switch m := v.(type) {
	case map[string]interface{}:
		return m, true
	case bson.M:
		return map[string]interface{}(m), true
	default:
		return nil, false
	}
}

// matchQuery returns true if doc matches the filter. Supports $gt, $gte, $lt, $lte, $eq, $ne, $in, $nin, and $regex.
func matchQuery(doc map[string]interface{}, filter map[string]interface{}) bool {
	for key, filterVal := range filter {
		docVal := doc[key]
		if filterVal == nil {
			if docVal != nil {
				return false
			}
			continue
		}
		// Operator map: e.g. map[string]interface{}{ "$gt": 10 }
		if opMap, ok := filterVal.(map[string]interface{}); ok {
			if !matchOperators(docVal, opMap) {
				return false
			}
			continue
		}
		// $regex: can be passed as *regexp.Regexp from Go callers
		if re, ok := filterVal.(*regexp.Regexp); ok {
			s, _ := docVal.(string)
			if !re.MatchString(s) {
				return false
			}
			continue
		}
		// Exact match
		if !valueEqual(docVal, filterVal) {
			return false
		}
	}
	return true
}

func valueEqual(a, b interface{}) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	// Normalize number types for comparison
	if fa, ok := toFloat(a); ok {
		if fb, ok := toFloat(b); ok {
			return fa == fb
		}
	}
	return a == b
}

func toFloat(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	default:
		return 0, false
	}
}

func matchOperators(docVal interface{}, opMap map[string]interface{}) bool {
	for op, opVal := range opMap {
		switch op {
		case "$gt":
			if !compare(docVal, opVal, 1) {
				return false
			}
		case "$gte":
			if compare(docVal, opVal, -1) {
				return false
			}
		case "$lt":
			if compare(docVal, opVal, 1) || valueEqual(docVal, opVal) {
				return false
			}
		case "$lte":
			if compare(docVal, opVal, 1) {
				return false
			}
		case "$eq":
			if !valueEqual(docVal, opVal) {
				return false
			}
		case "$ne":
			if valueEqual(docVal, opVal) {
				return false
			}
		case "$in":
			arr, ok := opVal.([]interface{})
			if !ok {
				return false
			}
			found := false
			for _, v := range arr {
				if valueEqual(docVal, v) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		case "$nin":
			arr, ok := opVal.([]interface{})
			if !ok {
				return true
			}
			for _, v := range arr {
				if valueEqual(docVal, v) {
					return false
				}
			}
		case "$regex":
			// String pattern; optional $options for flags (e.g. "i" for case-insensitive)
			pattern, _ := opVal.(string)
			options := ""
			if o, ok := opMap["$options"]; ok {
				options, _ = o.(string)
			}
			if options == "i" {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return false
			}
			s, _ := docVal.(string)
			if !re.MatchString(s) {
				return false
			}
		}
	}
	return true
}

// compare returns true if docVal > opVal (direction 1), or docVal < opVal (direction -1). Uses numeric comparison.
func compare(docVal, opVal interface{}, direction int) bool {
	d, ok1 := toFloat(docVal)
	o, ok2 := toFloat(opVal)
	if !ok1 || !ok2 {
		return false
	}
	if direction > 0 {
		return d > o
	}
	return d < o
}
