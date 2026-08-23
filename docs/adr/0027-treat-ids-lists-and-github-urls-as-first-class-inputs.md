# Treat IDs, lists, and GitHub URLs as first-class inputs

Install and targeted update commands accept direct official plugin IDs with optional exact versions, Plugin List paths, and explicit GitHub URLs, and may combine these input kinds in one invocation. Plugman classifies existing local files as lists, supported `https://github.com/...` forms as GitHub inputs, and remaining valid identifiers as official plugin IDs, reporting missing file-like inputs clearly rather than silently reinterpreting them.
