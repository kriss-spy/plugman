# Preflight batches and restore per plugin

Plugman validates all declarations and fetches all required releases before mutating the vault, so a preflight failure changes nothing. Application then proceeds one plugin at a time; the first failure restores that plugin, stops the command, retains earlier successes, and reports the resulting state. Rerunning skips satisfied entries and naturally resumes without a whole-batch transaction.
