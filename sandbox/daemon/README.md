# sandboxd

On macOS, `sandboxd` always signs rewritten binaries with the hardened-runtime
option. It reads entitlements from the original binary only after:

1. `codesign --verify --strict` succeeds; and
2. `codesign -d --entitlements :-` produces valid plist output.

The daemon copies only these entitlements:

- `com.apple.security.cs.allow-jit`
- `com.apple.security.cs.allow-unsigned-executable-memory`
- `com.apple.security.get-task-allow`

`get-task-allow` is never copied merely because it exists on the source. The
daemon must also be started with:

```text
--allow-get-task-allow
```

The launcher maps this from `SANDBOX_ALLOW_GET_TASK_ALLOW=1`. Without that
explicit opt-in, rewritten binaries receive `get-task-allow=false`.

Invalid, unsigned, or unverifiable original signatures result in all three
entitlements being false. Hardened runtime remains enabled in every successful
macOS rewrite, and no `DYLD_INSERT_LIBRARIES` mechanism is used.