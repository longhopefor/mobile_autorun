# Third-party materials

The root MIT license covers this project's original code and documentation.
Third-party dependencies and reference documents retain their own licenses.

## Mobilerun Portal

- Upstream: <https://github.com/droidrun/mobilerun-portal>
- Protocol reference revision: `cc3fa91b15847ca9f7cc9a5afdd6b8e1db7e4ec2`
- Vendored documentation: `docs/vendor/portal-local-api.md` and
  `docs/vendor/portal-reverse-connection.md`.
- Upstream license: GNU Affero General Public License v3.0, reproduced in
  [docs/vendor/portal-LICENSE](docs/vendor/portal-LICENSE).

These documentation snapshots are reference materials, excluded from the root
MIT grant. The Portal APK is a separate Android application and is not bundled
in this repository or the server image. Previous Kotlin source snapshots are
not included in this public source tree. A modified Portal APK, including the
optional native media-index extension, must be obtained and maintained separately.

## Dependencies

Go dependencies and versions are recorded in `go.mod` / `go.sum`;
frontend dependencies and versions are recorded in `frontend/package.json` /
`frontend/package-lock.json`. Their upstream licenses continue to apply.
Docker builds also install Debian packages, FFmpeg and Noto CJK fonts under
their respective upstream licenses.
