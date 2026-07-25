# Virtual Me documentation site

The site is an isolated Astro package. From the repository root:

```console
npm ci
npm ci --prefix docs
./cli.sh docs dev
./cli.sh docs build
```

The first command prepares the root quality gate. The second installs the
isolated site toolchain. Ordinary builds and checks are offline after setup.

Browser tests require a separate explicit setup:

```console
npm --prefix docs exec playwright install chromium
npm --prefix docs run test:browser
```

CI may add `--with-deps` to browser installation. A normal docs build does not
need a browser.

Analytics is disabled by default. A maintainer may edit
`docs/src/config/site.ts` or set `PUBLIC_GA_MEASUREMENT_ID` to a valid public
`G-...` identifier, after checking applicable disclosure and consent duties.

Publication rebuilds the site and force-pushes branch-root output to the
orphan `docs` branch. After its first successful run, configure repository
Pages once to deploy from branch `docs`, folder `/ (root)`. The expected site
is <https://mayanklahiri.github.io/virtualme/>.
