# SDM Vault Example

Try permission grants, typed Vault entries, and reviewed requests with disposable data. Use a Vault-capable **Stream Dock Manager 0.1.1 or later** build.

[Install](#install-and-try-it) · [Vault exercises](#vault-exercises) ·
[Build the ZIP](#build-and-test)

## Install and try it

1. In Manager, choose **Plugins → Import plugin**, select the Example ZIP, then enable it.
2. Create disposable entries in **Vault → Entries**. Link only the slots you want to try during package review or from the installed plugin's permissions. Never enter a real secret in the property inspector.
3. Save an example action on a button, or review and import the suggested profile. Try the action, revoke its grant, then run it again to confirm it no longer has access.

## Vault exercises

| Action                     | What to try                                                                                                                                                                                                         |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Check permission grants    | Compare granted and declared scopes before and after changing a grant                                                                                                                                               |
| Read a setup slot          | Link text, an API key, a bearer token, or a credential. Approve direct sharing in Manager for protected values. The action confirms the read without displaying the value                                           |
| Write a private secret     | Approve one secret kind. Leave **Current revision** empty to create, then enter the reported revision to rotate. An old revision reports a conflict. Find the private slot under **Vault → Plugins**                |
| Review a clipboard summary | Use disposable clipboard text and credentials. Select **pick for use** or **use setup slot**, review the destination and text in Manager, then Cancel or explicitly Send. Copy requires a separate grant and action |
| Read a shared location     | Link and approve a Location, then revoke the grant to compare success and denial                                                                                                                                    |
| Browse metadata            | Approve browsing and list approved names. Browsing and using an entry are separate permissions                                                                                                                      |
| Check denial and recovery  | Lock Vault or revoke a grant, then unlock or reapprove. References follow renames, but deleted entries and imports into another Vault need relinking                                                                |

The summary exercise uses a reserved test origin. The webhook viewer uses only local loopback.

### Manager variable exercises

Extract `examples/vault-variables.zip` from the plugin ZIP and import it in **Macros**. Open a macro, use the Vault picker to replace its placeholder, then save and bind it. Start with disposable clipboard text and files.

| Macro                  | Example input                                                                           |
| ---------------------- | --------------------------------------------------------------------------------------- |
| Copy a Vault scalar    | Unprotected text, integer, and boolean entries, such as `hello`, `42`, and `true`       |
| Reveal a Vault path    | A disposable File or Folder                                                             |
| Compare clipboard JSON | An unprotected JSON entry, such as `{"example":1}`; or a Location's name or coordinates |

Protected entries and credentials cannot be interpolated as scalars. Imported references need relinking.

For plugin-side formatting, link a disposable Text entry to **Read a setup slot** and grant access. Use **Copy reference** in Vault, then paste it after `Hello ` in **Display text**. Press the action to show the result. The plugin keeps the reference in settings, not the expanded value. This requires a Manager build that supports text references.

## HTTP and HTTPS webhook viewer

1. Bind and press **Start webhook viewer**, then open [the local viewer](http://127.0.0.1:39480/).
2. Try **Send from plugin**, approve its HTTPS grant, then press again. Refresh the viewer for `POST /webhooks/plugin`.
3. Try **Send through Manager** and review its separate HTTPS and popup grants. Choose **Send ping** in the review. Verify that Cancel or a revoked grant creates no delivery.
4. Select **http** in the inspector to repeat over plain HTTP with a separate grant.

The viewer binds to IPv4 loopback only. HTTP uses port `39480`; HTTPS uses `39481` and validates against the listener's session certificate. The listener accepts only a fixed JSON ping. Its most recent 64 accepted requests are shown without secret values, and restart clears the history.

To call the local HTTPS example from a terminal, download the public session certificate from the viewer and run this from its download directory:

```sh
curl --cacert sdm-example.pem -H 'Content-Type: application/json' \
  --data '{"event":"ping"}' https://127.0.0.1:39481/webhooks/example
```

An accepted ping returns HTTP 204. The plugin is responsible for honoring its direct-send grant.

## Build and test

From the Plugins root, select the matching [Go SDK](../../../DEVELOPMENT.md#sdk-access-and-local-development), then run:

```sh
go test ./internal/plugins/sdm-vault-example ./internal/catalog ./tools/pluginbuild
make package PLUGIN=sdm-vault-example
```

Import the ZIP from `dist/packages/<os>/<arch>/`. The regular catalog and package builder both include this example.
