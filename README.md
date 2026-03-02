# gomingleDB

Go port of [MingleDB](https://github.com/marcuwynu23/mingledb): a lightweight, file-based NoSQL database with BSON serialization, zlib compression, schema validation, and basic authentication.

## Features

| Feature | Description |
|--------|-------------|
| **User authentication** | Register, login, logout, session tracking (SHA256 hashing) |
| **Schema definition** | Required fields, types (`string`, `number`), unique constraints |
| **Query operators** | `$gt`, `$gte`, `$lt`, `$lte`, `$eq`, `$ne`, `$in`, `$nin`, `$regex` |
| **Compression** | BSON + zlib for compact storage |
| **Flat file storage** | One `.mgdb` file per collection; same format as mingleDB |

## Installation

```bash
go get gomingleDB
```

## Usage

```go
package main

import "gomingleDB"

func main() {
    db := gomingleDB.New("./data") // optional: default is "./mydb"

    // 1. Register & login
    _ = db.RegisterUser("admin", "secure123")
    _ = db.Login("admin", "secure123")

    // 2. Define schema
    db.DefineSchema("users", gomingleDB.SchemaDefinition{
        "name":  {Type: "string", Required: true},
        "email": {Type: "string", Required: true, Unique: true},
        "age":   {Type: "number"},
    })

    // 3. Insert
    _ = db.InsertOne("users", map[string]interface{}{
        "name":  "Wayne",
        "email": "wayne@mingle.com",
        "age":   25.0,
    })

    // 4. Read
    all, _ := db.FindAll("users")
    one, _ := db.FindOne("users", map[string]interface{}{"email": "wayne@mingle.com"})
    rangeDocs, _ := db.Find("users", map[string]interface{}{
        "age": map[string]interface{}{"$gte": 18.0, "$lt": 30.0},
    })

    // 5. Update
    db.UpdateOne("users", map[string]interface{}{"name": "Wayne"}, map[string]interface{}{"age": 26.0})

    // 6. Delete
    db.DeleteOne("users", map[string]interface{}{"email": "wayne@mingle.com"})

    // 7. Logout
    db.Logout("admin")
}
```

## Query operators

| Operator | Description |
|----------|-------------|
| `$gt`, `$gte`, `$lt`, `$lte` | Greater/less than (or equal) |
| `$eq`, `$ne` | Equals / not equals |
| `$in`, `$nin` | In list / not in list |
| `$regex` | Regex (use `$options: "i"` for case-insensitive) |

For regex match on a field without operators, pass a `*regexp.Regexp` as the filter value:

```go
re := regexp.MustCompile("(?i)wayne")
docs, _ := db.Find("users", map[string]interface{}{"name": re})
```

## API

- `New(dbDir string) *MingleDB`
- `Reset() error` — wipe all collections and schemas
- `DefineSchema(collection string, schema SchemaDefinition)`
- `RegisterUser(username, password string) error`
- `Login(username, password string) error`
- `IsAuthenticated(username string) bool`
- `Logout(username string)`
- `InsertOne(collection string, doc map[string]interface{}) error`
- `FindAll(collection string) ([]map[string]interface{}, error)`
- `Find(collection string, filter map[string]interface{}) ([]map[string]interface{}, error)`
- `FindOne(collection string, filter map[string]interface{}) (map[string]interface{}, error)`
- `UpdateOne(collection string, query, update map[string]interface{}) (bool, error)`
- `DeleteOne(collection string, query map[string]interface{}) (bool, error)`

## File format

Same as [mingleDB](https://github.com/marcuwynu23/mingledb): header `MINGLEDBv1`, 4-byte meta length, JSON metadata, then for each document: 4-byte length + zlib-compressed BSON. Collections use the `.mgdb` extension.

## Tests

```bash
go test ./...
```

## License

MIT
