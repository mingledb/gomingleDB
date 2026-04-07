// Package tests holds end-to-end tests for gomingleDB, mirroring mingledb/tests/test.js.
package tests

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mingledb/gomingleDB"
)

func tmpDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "db")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// ---- AUTHENTICATION ----
func TestRegisterLoginAndLogout(t *testing.T) {
	db := gomingleDB.New(tmpDir(t))
	defer db.Reset()

	if err := db.RegisterUser("admin", "secure123"); err != nil {
		t.Fatal("register:", err)
	}
	if err := db.Login("admin", "secure123"); err != nil {
		t.Fatal("login:", err)
	}
	if !db.IsAuthenticated("admin") {
		t.Error("expected authenticated after login")
	}

	db.Logout("admin")
	if db.IsAuthenticated("admin") {
		t.Error("expected not authenticated after logout")
	}
}

// ---- Schema definition ----
func TestDefineSchemaAndInsertDocuments(t *testing.T) {
	db := gomingleDB.New(tmpDir(t))
	defer db.Reset()

	db.DefineSchema("users", gomingleDB.SchemaDefinition{
		"name":  {Type: "string", Required: true},
		"email": {Type: "string", Required: true, Unique: true},
		"age":   {Type: "number"},
	})

	if err := db.InsertOne("users", map[string]interface{}{"name": "Cloud", "email": "cloud@seed.com", "age": float64(25)}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertOne("users", map[string]interface{}{"name": "Alice", "email": "alice@example.com", "age": float64(30)}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertOne("users", map[string]interface{}{"name": "Bob", "email": "bob@example.com", "age": float64(17)}); err != nil {
		t.Fatal(err)
	}

	all, err := db.FindAll("users")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 documents, got %d", len(all))
	}
}

// ---- Uniqueness & validation ----
func TestRejectDuplicateEmailOrMissingRequired(t *testing.T) {
	db := gomingleDB.New(tmpDir(t))
	defer db.Reset()

	db.DefineSchema("users", gomingleDB.SchemaDefinition{
		"name":  {Type: "string", Required: true},
		"email": {Type: "string", Required: true, Unique: true},
	})

	if err := db.InsertOne("users", map[string]interface{}{"name": "A", "email": "a@a.com"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertOne("users", map[string]interface{}{"name": "B", "email": "a@a.com"}); err == nil {
		t.Error("expected error for duplicate email (unique violation)")
	} else if !errors.Is(err, gomingleDB.ErrValidation) && !strings.Contains(err.Error(), "unique") {
		t.Errorf("expected validation/unique error: %v", err)
	}
	if err := db.InsertOne("users", map[string]interface{}{"email": "missingname@x.com"}); err == nil {
		t.Error("expected error for missing required field")
	} else if !errors.Is(err, gomingleDB.ErrValidation) && !strings.Contains(err.Error(), "required") {
		t.Errorf("expected required/validation error: %v", err)
	}
}

// ---- Read operations ----
func TestFindAllFindOneRegexRangeAndIn(t *testing.T) {
	db := gomingleDB.New(tmpDir(t))
	defer db.Reset()

	db.DefineSchema("users", gomingleDB.SchemaDefinition{
		"name":  {Type: "string", Required: true},
		"email": {Type: "string", Required: true, Unique: true},
		"age":   {Type: "number"},
	})

	db.InsertOne("users", map[string]interface{}{"name": "Cloud", "email": "cloud@seed.com", "age": float64(25)})
	db.InsertOne("users", map[string]interface{}{"name": "Alice", "email": "alice@example.com", "age": float64(30)})
	db.InsertOne("users", map[string]interface{}{"name": "Bob", "email": "bob@example.com", "age": float64(17)})

	all, err := db.FindAll("users")
	if err != nil || len(all) != 3 {
		t.Fatalf("FindAll: err=%v len=%d", err, len(all))
	}

	alice, err := db.FindOne("users", map[string]interface{}{"email": "alice@example.com"})
	if err != nil || alice["name"] != "Alice" {
		t.Errorf("FindOne alice: err=%v name=%v", err, alice)
	}

	regexMatch, err := db.Find("users", map[string]interface{}{"name": regexp.MustCompile("(?i)clo")})
	if err != nil || len(regexMatch) != 1 || regexMatch[0]["name"] != "Cloud" {
		t.Errorf("Find with regex: err=%v len=%d %v", err, len(regexMatch), regexMatch)
	}

	ageRange, err := db.Find("users", map[string]interface{}{
		"age": map[string]interface{}{"$gte": float64(18), "$lt": float64(60)},
	})
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, d := range ageRange {
		names[d["name"].(string)] = true
	}
	if !names["Cloud"] || !names["Alice"] || names["Bob"] {
		t.Errorf("age range expected Cloud and Alice only; got %v", names)
	}

	emailIn, err := db.Find("users", map[string]interface{}{
		"email": map[string]interface{}{"$in": []interface{}{"cloud@seed.com", "a@b.com"}},
	})
	if err != nil || len(emailIn) != 1 || emailIn[0]["email"] != "cloud@seed.com" {
		t.Errorf("Find $in: err=%v len=%d %v", err, len(emailIn), emailIn)
	}
}

// ---- Update ----
func TestUpdateOneModifiesMatchingRecord(t *testing.T) {
	db := gomingleDB.New(tmpDir(t))
	defer db.Reset()

	db.DefineSchema("users", gomingleDB.SchemaDefinition{
		"name":  {Type: "string", Required: true},
		"email": {Type: "string", Required: true, Unique: true},
		"age":   {Type: "number"},
	})
	db.InsertOne("users", map[string]interface{}{"name": "Alice", "email": "alice@example.com", "age": float64(30)})

	updated, err := db.UpdateOne("users", map[string]interface{}{"name": "Alice"}, map[string]interface{}{"age": float64(31)})
	if err != nil || !updated {
		t.Fatalf("UpdateOne: updated=%v err=%v", updated, err)
	}

	check, err := db.FindOne("users", map[string]interface{}{"name": "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if check["age"] != float64(31) {
		t.Errorf("expected age 31, got %v", check["age"])
	}
}

// ---- Delete ----
func TestDeleteOneRemovesDocument(t *testing.T) {
	db := gomingleDB.New(tmpDir(t))
	defer db.Reset()

	db.DefineSchema("users", gomingleDB.SchemaDefinition{
		"name":  {Type: "string", Required: true},
		"email": {Type: "string", Required: true, Unique: true},
	})
	db.InsertOne("users", map[string]interface{}{"name": "Alice", "email": "alice@example.com"})

	deleted, err := db.DeleteOne("users", map[string]interface{}{"email": "alice@example.com"})
	if err != nil || !deleted {
		t.Fatalf("DeleteOne: deleted=%v err=%v", deleted, err)
	}

	all, err := db.FindAll("users")
	if err != nil || len(all) != 0 {
		t.Errorf("expected 0 documents after delete, got %d", len(all))
	}
}
