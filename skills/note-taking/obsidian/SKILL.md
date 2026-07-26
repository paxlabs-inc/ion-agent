---
name: obsidian
description: Read, search, create, and edit notes in the Obsidian vault.
platforms: [linux, macos, windows]
---

# Obsidian Vault

This skill handles filesystem-level Obsidian vault operations: reading notes, listing notes, searching note files, creating notes, appending content, and adding wikilinks.

## Vault path

Determine and use a known or resolved vault path before calling any file tools.

The established vault-path convention relies on the `OBSIDIAN_VAULT_PATH` environment variable, for example from `${ION_HOME:-~/.ion}/.env`. If that variable is unset, fall back to `~/Documents/Obsidian Vault`.

File tools do not expand shell variables. Avoid passing paths containing `$OBSIDIAN_VAULT_PATH` to `read_file`, `write_file`, `patch`, or `search_files`; resolve the vault path first and provide a concrete absolute path. Vault paths may include spaces, which is another reason to prefer file tools over shell commands.

If the vault path is unknown, `terminal` is fine for resolving `OBSIDIAN_VAULT_PATH` or verifying whether the fallback path exists. Once the path is confirmed, switch to file tools.

## Read a note

Use `read_file` with the resolved absolute path to the note. This is preferable to `cat` since it provides line numbers and pagination.

## List notes

Use `search_files` with `target: "files"` and the resolved vault path. This is preferable to `find` or `ls`.

- To list all markdown notes, use `pattern: "*.md"` under the vault path.
- To list a subfolder, search under that subfolder's absolute path.

## Search

Use `search_files` for both filename and content searches. This is preferable to `grep`, `find`, or `ls`.

- For filenames, use `search_files` with `target: "files"` and a filename `pattern`.
- For note contents, use `search_files` with `target: "content"`, the content regex as `pattern`, and `file_glob: "*.md"` when you want to restrict matches to markdown notes.

## Create a note

Use `write_file` with the resolved absolute path and the full markdown content. This is preferable to shell heredocs or `echo` since it avoids shell quoting issues and returns structured results.

## Append to a note

Prefer a native file-tool workflow when it's practical:

- Read the target note with `read_file`.
- Use `patch` for an anchored append when there is stable context, such as adding a section after an existing heading or appending before a known trailing block.
- Use `write_file` when rewriting the whole note is clearer than constructing a fragile patch.

For an anchored append with `patch`, replace the anchor with the anchor plus the new content.

For a simple append with no stable context, `terminal` is acceptable if it's the clearest safe option.

## Targeted edits

Use `patch` for focused note changes when the current content provides stable context. This is preferable to shell text rewriting.

## Wikilinks

Obsidian connects notes using `[[Note Name]]` syntax. When creating notes, use these to link related content.
