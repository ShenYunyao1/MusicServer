# jsmediatags 3.9.7

Source: https://github.com/aadsm/jsmediatags (npm package `jsmediatags@3.9.7`,
`dist/jsmediatags.min.js`). License: BSD-3-Clause, see `jsmediatags-LICENSE.md`.
The bundle is embedded in the server and served locally; no CDN is required.

Local patches: COMM/COM, USLT/ULT and TXXX/TXX described UTF-16 text inherit the
description's UTF-16BE BOM for the following string when it lacks its own BOM.
Upstream 3.9.7 defaults the second string to UTF-16LE. ID3v2.2 TXX also uses the
TXXX frame reader so its description and value are both available. Regression coverage is in
`server/tests/local-preview.test.cjs`. The rest of the distribution is unchanged.
