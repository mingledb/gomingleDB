# Publishing gomingleDB

gomingleDB is a Go module. You can “publish” it by pushing to GitHub and (optionally) using a proper module path so others can `go get` it.

## Option A: Use as a dependency via replace (current setup)

- **mingledb-cli** (and other local projects) can depend on gomingleDB with a `replace` in `go.mod`:
  ```go
  replace github.com/mingledb/gomingleDB => ../gomingleDB
  require github.com/mingledb/gomingleDB v0.0.0
  ```
- No need to change gomingleDB. For **mingledb-cli** CI, the release workflow checks out this repo next to mingledb-cli so the replace path exists.

## Option B: Publish on GitHub so others can `go get` it

1. **Push the repo to GitHub**  
   e.g. `github.com/mingledb/gomingleDB`.

2. **Use a module path that matches the repo**  
   In `go.mod`, set:
   ```go
   module github.com/mingledb/gomingleDB
   ```
   (Replace with your actual GitHub user/org and repo name.)

3. **Tag a version**
   ```bash
   git tag v0.0.1
   git push origin v0.0.1
   ```

4. **Others can then install**
   ```bash
   go get github.com/mingledb/gomingleDB@v0.0.1
   ```

5. **mingledb-cli (or any consumer)** can depend on it without `replace`:
   ```go
   require github.com/mingledb/gomingleDB v0.0.1
   ```
   and remove the `replace` line. CI would then work without checking out gomingleDB (Go would fetch it from the proxy).

## Summary

| Goal                         | Action |
|-----------------------------|--------|
| Release mingledb-cli binaries | Tag `v*` in mingledb-cli; ensure gomingleDB repo exists under same owner. See mingledb-cli’s **PUBLISH.md**. |
| Let others use gomingleDB    | Change module path in gomingleDB to `github.com/owner/gomingleDB`, push, tag (e.g. `v0.0.1`). |
