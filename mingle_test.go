package gomingleDB

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "db")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRegisterLoginLogout(t *testing.T) {
	db := New(tempDir(t))
	defer db.Reset()

	if err := db.RegisterUser("admin", "secure123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Login("admin", "secure123"); err != nil {
		t.Fatal(err)
	}
	if !db.IsAuthenticated("admin") {
		t.Error("expected authenticated")
	}
	db.Logout("admin")
	if db.IsAuthenticated("admin") {
		t.Error("expected not authenticated after logout")
	}
}

func TestDefineSchemaAndInsert(t *testing.T) {
	db := New(tempDir(t))
	defer db.Reset()

	db.DefineSchema("users", SchemaDefinition{
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
		t.Errorf("expected 3 docs, got %d", len(all))
	}
}

func TestRejectDuplicateOrMissingRequired(t *testing.T) {
	db := New(tempDir(t))
	defer db.Reset()

	db.DefineSchema("users", SchemaDefinition{
		"name":  {Type: "string", Required: true},
		"email": {Type: "string", Required: true, Unique: true},
	})

	if err := db.InsertOne("users", map[string]interface{}{"name": "A", "email": "a@a.com"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertOne("users", map[string]interface{}{"name": "B", "email": "a@a.com"}); err == nil {
		t.Error("expected unique violation")
	} else if !errors.Is(err, ErrValidation) && !strings.Contains(err.Error(), "unique") {
		t.Errorf("expected validation/unique error: %v", err)
	}
	if err := db.InsertOne("users", map[string]interface{}{"email": "missingname@x.com"}); err == nil {
		t.Error("expected required field error")
	} else if !errors.Is(err, ErrValidation) && !strings.Contains(err.Error(), "required") {
		t.Errorf("expected required/validation error: %v", err)
	}
}

func TestFindAllFindOneRegexRangeIn(t *testing.T) {
	db := New(tempDir(t))
	defer db.Reset()

	db.DefineSchema("users", SchemaDefinition{
		"name":  {Type: "string", Required: true},
		"email": {Type: "string", Required: true, Unique: true},
		"age":   {Type: "number"},
	})

	db.InsertOne("users", map[string]interface{}{"name": "Cloud", "email": "cloud@seed.com", "age": float64(25)})
	db.InsertOne("users", map[string]interface{}{"name": "Alice", "email": "alice@example.com", "age": float64(30)})
	db.InsertOne("users", map[string]interface{}{"name": "Bob", "email": "bob@example.com", "age": float64(17)})

	all, _ := db.FindAll("users")
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}

	alice, err := db.FindOne("users", map[string]interface{}{"email": "alice@example.com"})
	if err != nil || alice["name"] != "Alice" {
		t.Errorf("findOne alice: err=%v name=%v", err, alice)
	}

	regexMatch, err := db.Find("users", map[string]interface{}{"name": regexp.MustCompile("(?i)clo")})
	if err != nil || len(regexMatch) != 1 || regexMatch[0]["name"] != "Cloud" {
		t.Errorf("regex find: err=%v len=%d %v", err, len(regexMatch), regexMatch)
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
		t.Errorf("age range expected Cloud, Alice; got %v", names)
	}

	emailIn, err := db.Find("users", map[string]interface{}{
		"email": map[string]interface{}{"$in": []interface{}{"cloud@seed.com", "a@b.com"}},
	})
	if err != nil || len(emailIn) != 1 || emailIn[0]["email"] != "cloud@seed.com" {
		t.Errorf("$in: err=%v len=%d %v", err, len(emailIn), emailIn)
	}
}

func TestUpdateOne(t *testing.T) {
	db := New(tempDir(t))
	defer db.Reset()

	db.DefineSchema("users", SchemaDefinition{
		"name":  {Type: "string", Required: true},
		"email": {Type: "string", Required: true, Unique: true},
		"age":   {Type: "number"},
	})
	db.InsertOne("users", map[string]interface{}{"name": "Alice", "email": "alice@example.com", "age": float64(30)})

	updated, err := db.UpdateOne("users", map[string]interface{}{"name": "Alice"}, map[string]interface{}{"age": float64(31)})
	if err != nil || !updated {
		t.Fatalf("updateOne: updated=%v err=%v", updated, err)
	}
	check, _ := db.FindOne("users", map[string]interface{}{"name": "Alice"})
	if check["age"] != float64(31) {
		t.Errorf("expected age 31, got %v", check["age"])
	}
}

func TestDeleteOne(t *testing.T) {
	db := New(tempDir(t))
	defer db.Reset()

	db.DefineSchema("users", SchemaDefinition{
		"name":  {Type: "string", Required: true},
		"email": {Type: "string", Required: true, Unique: true},
	})
	db.InsertOne("users", map[string]interface{}{"name": "Alice", "email": "alice@example.com"})

	deleted, err := db.DeleteOne("users", map[string]interface{}{"email": "alice@example.com"})
	if err != nil || !deleted {
		t.Fatalf("deleteOne: deleted=%v err=%v", deleted, err)
	}
	all, _ := db.FindAll("users")
	if len(all) != 0 {
		t.Errorf("expected 0 docs, got %d", len(all))
	}
}

func TestReset(t *testing.T) {
	db := New(tempDir(t))
	db.InsertOne("users", map[string]interface{}{"name": "X", "email": "x@x.com"})
	if err := db.Reset(); err != nil {
		t.Fatal(err)
	}
	all, _ := db.FindAll("users")
	if len(all) != 0 {
		t.Errorf("after reset expected 0, got %d", len(all))
	}
}
