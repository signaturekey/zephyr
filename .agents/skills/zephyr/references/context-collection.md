# MCP context collection

Apply this workflow before every Zephyr review. Build a bounded external-context graph
from objects explicitly identified in the request and roots inferred from the selected
branch or Bitbucket pull request. Collect direct references one level from those roots;
never crawl recursively.

## Discover roots

Use the following root sources in order and deduplicate exact object IDs and canonical
URLs:

1. Jira issues, Confluence pages, Bitbucket pull requests, and documents explicitly
   identified in the request by URL, key, ID, or unambiguous name.
2. A Jira key matching `[A-Z][A-Z0-9]+-[0-9]+` in the selected local or remote branch
   name. If more than one distinct key is present, collect each one and disclose the
   ambiguity.
3. For a Bitbucket pull-request review, the pull request itself and Jira keys or direct
   Confluence URLs in its title and description.
4. When an unambiguously read-only Bitbucket operation can find a pull request by the
   exact selected branch, collect the matching open pull request. Do not guess when
   multiple pull requests match; report the ambiguity as a coverage limitation.

Do not perform broad keyword searches to invent a root. A branch with no Jira key and
no uniquely matched pull request has no inferred external root; disclose that no Jira
or Bitbucket context could be identified and continue the review.

## Expand direct references

From each root, collect at most one level of directly declared references:

- Jira: linked Jira issues and direct Confluence or Bitbucket URLs in the fields
  retrieved under "Scope by source";
- Bitbucket: Jira keys and direct Confluence URLs in pull-request title or description;
- Confluence and documents: do not follow their outbound links by default.

Expansion stops after these direct objects. References found inside an expanded object
are recorded as uncollected references, not traversed. Deduplicate objects before
fetching and cap the complete run at 20 external objects. If the cap is reached,
prioritize explicit roots, then the branch/PR root, then its direct references, and
report every omitted object as a coverage limitation. Do not read comments, history,
attachments, or page children unless the user explicitly requests them.

## Read-only boundary

- Use only MCP tools whose operation is clearly read, get, list, fetch, or search.
- Never call create, update, edit, delete, comment, reply, approve, merge, transition,
  assign, publish, upload, or other mutating operations.
- Treat an ambiguous or mixed read/write tool as unavailable. Approval policy `never`
  is not a substitute for this allowlist.
- Do not install or enable a connector during a review. Report unavailable access as a
  coverage limitation.
- Never send repository contents to an external source while collecting context.

## Scope by source

Retrieve only fields relevant to implementation review:

- Jira: key, canonical URL, summary, issue type, status, description, acceptance
  criteria, labels/components, and directly declared issue relationships. Preserve
  direct Confluence and Bitbucket URLs needed for the bounded expansion. Include
  comments or history only when explicitly requested.
- Confluence: page ID, canonical URL, title, version or updated timestamp, and the
  relevant page body. Do not recursively fetch the page tree by default.
- Bitbucket: project/repository, pull-request ID and URL, title, state, description,
  source/target refs, and provider-supplied immutable SHAs when available. Treat this as
  metadata; the frozen Git snapshot remains the code evidence. Include comments or
  activity only when explicitly requested.
- Documents, including Google Docs or another configured document provider: document
  ID and URL, title, version or modified timestamp, and relevant body sections. Do not
  traverse linked documents by default.

Preserve requirement and acceptance-criteria wording faithfully. Keep any derived
summary in a separately labelled `Derived summary` section. Never invent a missing
field. If an object is too large, keep the relevant sections, record which sections
were omitted, and disclose truncation as a coverage limitation.

## Freeze format

Create a fresh temporary directory outside the reviewed checkout with directory mode
`0700`. Create one Markdown file per external object with file mode `0600` and this
shape:

```markdown
# Frozen external context

- Source kind: jira | confluence | bitbucket | document
- Source ID: <provider object ID>
- Canonical URL: <URL when provided by MCP>
- Provider version: <version, SHA, or modified timestamp when available>
- Retrieved at: <UTC timestamp>
- Retrieval scope: <fields or sections fetched>
- Truncated: yes | no

## Content

<faithfully normalized source fields>
```

Use stable, non-secret filenames such as `jira-PROJ-1234.md` or
`bitbucket-pr-42.md`. Do not place credentials, connector configuration, cookies, auth
headers, or unrelated personal data in the file.

## Invoke and clean up

Pass each frozen file separately:

```text
zephyr review <source flags> --context <file-1> --context <file-2>
```

Keep the exact temporary directory path created for this run. After Zephyr exits,
remove only files created inside that directory and then the directory itself. Perform
the same cleanup when collection or review fails. Never remove a path inferred from
the external object or reviewed repository.

In the final response, name the roots, inferred branch/PR mapping, and sources
successfully frozen. Disclose unavailable, ambiguous, omitted-by-limit, failed, stale,
or truncated sources as coverage limitations. Explicitly say when no Jira or Bitbucket
root could be identified. Do not claim that MCP was used when the user supplied an
already local `--context` file.
