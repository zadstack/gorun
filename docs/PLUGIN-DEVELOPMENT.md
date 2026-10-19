# Build your first GoRun plugin (extension)

> **بالعربي:** في GoRun، الـPlugin اسمه **Extension**، وهو خدمة HTTP خارجية، مش مكتبة Go بتتثبت جوه الـBinary. الدليل ده بيشرح إنشاء الإضافة وتجربتها ونشرها، خطوة بخطوة. للمرجع الكامل: [EXTENSIONS.md](EXTENSIONS.md).

This hands-on tutorial covers the extension API that exists today. It does **not** describe a native Go dynamic plugin loader or a plugin marketplace. GoRun Full currently uses an HTTP hook bridge. GoRun Edge compatibility is not yet implemented.

## 1. What you will build

A tiny Go HTTP service that receives signed GoRun events. The first plugin logs when content is published; it does not need write permissions.

Prerequisites: a running GoRun Full instance, Go 1.27 for the Go example, and access to **System → Extensions** with `extensions.manage`.

## 2. Create a package

Create a folder named `publish-notifier` and put this file in it:

**`manifest.json`**

```json
{
  "slug": "publish-notifier",
  "name": {"en": "Publish Notifier", "ar": "تنبيه النشر"},
  "version": "1.0.0",
  "description": {"en": "Receive published-content events", "ar": "استقبال أحداث النشر"},
  "author": "Your Name",
  "permissions": [],
  "hooks": [{"name": "content.published", "kind": "event"}],
  "endpoint": {"url": "https://your-plugin.example/gorun", "auth": "hmac"}
}
```

Replace the endpoint with the **real HTTPS URL** of your plugin service. The manifest declares which events it receives and which API scopes it needs. An empty `permissions` list means no REST API key is issued.

## 3. Implement the HTTP endpoint

A plugin can be written in Go, JavaScript, Python, or any language that serves HTTP. The following is a minimal Go example using only the standard library:

```go
package main

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "io"
    "log"
    "net/http"
    "os"
    "strconv"
    "time"
)

func main() {
    secret := os.Getenv("GORUN_EXTENSION_SECRET")
    if secret == "" { log.Fatal("GORUN_EXTENSION_SECRET is required") }
    http.HandleFunc("/gorun", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { http.Error(w, "method", 405); return }
        r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
        body, err := io.ReadAll(r.Body)
        if err != nil { http.Error(w, "body", 400); return }
        ts := r.Header.Get("X-GoRun-Timestamp")
        n, err := strconv.ParseInt(ts, 10, 64)
        if err != nil || time.Since(time.Unix(n, 0)) > 5*time.Minute ||
            time.Until(time.Unix(n, 0)) > 5*time.Minute {
            http.Error(w, "timestamp", 401); return
        }
        mac := hmac.New(sha256.New, []byte(secret))
        mac.Write([]byte(ts + "\n"))
        mac.Write(body)
        received, err := hex.DecodeString(r.Header.Get("X-GoRun-Signature"))
        if err != nil || !hmac.Equal(mac.Sum(nil), received) {
            http.Error(w, "signature", 401); return
        }
        // Avoid logging raw payloads: events can contain private content.
        log.Printf("verified delivery %s", r.Header.Get("X-GoRun-Delivery"))
        w.WriteHeader(http.StatusNoContent)
    })
    log.Fatal(http.ListenAndServe(":8787", nil))
}
```

For production, also persist `X-GoRun-Delivery` IDs to deduplicate retries, restrict who can access the endpoint, and use TLS at the public ingress. The HTTP bridge sends events at least once; your handler must be idempotent.

## 4. Install and test

1. Zip the package folder with `manifest.json` inside, or place the package under `<DataDir>/extensions/publish-notifier/`.
2. In GoRun, open **System → Extensions**, install the package, and securely save the HMAC secret shown **once**.
3. Configure that secret in the plugin service as `GORUN_EXTENSION_SECRET` and start the service.
4. Activate the extension in GoRun.
5. Publish a test post and inspect the delivery log on the extension page.

For a local-only demo, see `examples/extensions/hello-http`. It uses `http://127.0.0.1:8787/gorun`; private endpoint access requires `GORUN_ALLOW_PRIVATE_NET=1` in local development. Do not enable that exception on a public installation without understanding SSRF risks.

## 5. Add actions, filters and REST calls

- **Events** (`event`): asynchronous notifications such as `content.published`.
- **Actions** (`action`): asynchronous hooks such as `content.after_save`.
- **Filters** (`filter`): synchronous calls such as `content.before_save`; return JSON `{"value":{"title":"New title"}}` to modify supported input fields. Invalid responses or timeouts leave the original value unchanged.
- **REST**: request only the scopes you need in `permissions` (for example `content.view`); if permissions are granted, installation reveals a scoped API key once. Use `Authorization: Bearer <key>` with `/api/v1`. See [API.md](API.md).

**Security warning:** Hook deliveries may include entire private payloads regardless of API key scopes. Only install plugins you trust. HMAC authentication proves GoRun sent the delivery; it does not make an untrusted extension safe.

## 6. Publish and maintain

- Package `manifest.json` and optional `assets/` as a ZIP; the HTTP service must be deployed separately.
- Keep the plugin endpoint available; event/action deliveries are retried up to five times.
- Version your manifest with semver. Current updates use uninstall **without purge** followed by reinstall; record and replace the newly issued secret/key.
- Test both English and Arabic labels, empty settings, invalid signatures, duplicate deliveries, slow filters, and failure/recovery.
- Do not assume `admin_nav` creates sidebar items: currently those links appear on the extension's own admin page.

**Next:** [Full manifest and HTTP bridge reference](EXTENSIONS.md) · [Architecture and native build-time modules](ARCHITECTURE.md) · [Working hello extension](../examples/extensions/hello-http/).
