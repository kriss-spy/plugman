# Do not use lockfiles

Plugman will not generate or consume a lockfile. Plugin Lists remain the complete user-authored input: exact declarations reproduce a chosen version, while unversioned declarations may resolve differently as compatible releases appear. `install` leaves an already-installed unversioned plugin unchanged, and `update` is required to advance it; a new vault receives the newest compatible release available at installation time. This favors a simpler workflow and avoids additional generated state at the deliberate cost of cross-vault reproducibility for unversioned plugins.
