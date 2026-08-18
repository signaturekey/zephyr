# Reviewer quality regression

Deterministic golden fixtures validate precheck, evidence, deduplication, and report
mechanics after candidates already exist. They do not measure whether model reviewers
find the right P0-P3 issues.

Use the same adjudicated real cases for the old Zephyr and the Aether rewrite. Store
each run as a `forward-eval.schema.json` record in separate private directories, then
compare them:

```bash
go run ./evals/cmd/compare-quality \
  --baseline /private/evals/old-zephyr \
  --candidate /private/evals/aether-rewrite
```

The command requires identical case IDs and human baselines. It exits non-zero when
confirmed-finding recall or P0-P3 severity agreement decreases, or when the
false-positive rate increases. Do not use synthetic records as a product-quality
baseline and do not commit private code, review text, or credentials.
