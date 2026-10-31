# cfo dictation setup

`cfo dictation setup`

Set dictation up so the first press works at once, as the install does: download the speech engine and model the home pins (`config\voice.json`, the build's unless the home keeps its own), keep each only when it matches its pinned SHA-256 and links no networking library, put them in `caches\voice`, and remove an engine or model an earlier pin left; what is there already is not downloaded again.
A failure exits 1 and says why; the first dictation still fetches what is missing
