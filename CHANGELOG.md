# Changelog

## [0.1.0-alpha.14](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.13...v0.1.0-alpha.14) (2026-11-14)


### Features

* derive where a work order's time went from its events ([d8234bb](https://github.com/bkuberek/ropeshift/commit/d8234bb4917c8dba84a45c0416f40eb97c644214))
* keep each finished work order's timeline and delivery record ([1d39333](https://github.com/bkuberek/ropeshift/commit/1d39333a0f6977f4de10d24e4db4fee0859b7252))
* let a contract list the repositories its lanes read ([a67986d](https://github.com/bkuberek/ropeshift/commit/a67986d545456cbbfcb76f14789b1a92df67b2dd))
* let writer and review lanes read the repositories they build on ([9de8c2b](https://github.com/bkuberek/ropeshift/commit/9de8c2b5cb18894eb0c527eb09e85d0f288e3610))
* merge work after the work it depends on, across repositories ([d029b9d](https://github.com/bkuberek/ropeshift/commit/d029b9dc4e52bb0457af57400f7e97a04fb81951))
* read a repository manifest from the planning repository ([534c6ad](https://github.com/bkuberek/ropeshift/commit/534c6ade61fe737818869e2f705a56ac437e24e5))
* read a work order's timeline and delivery record from its records ([b907f54](https://github.com/bkuberek/ropeshift/commit/b907f542073d67f7f3ac0f16781ccb200ce92979))
* record the size of each lane's change with its result ([62cf3ac](https://github.com/bkuberek/ropeshift/commit/62cf3ac8ab71d920f7b1cb28593fdd64836c9e42))
* record when a worker's lanes stand at a lane cap ([fbebfff](https://github.com/bkuberek/ropeshift/commit/fbebfff1b2f6e3745b2c250f1385504b1f43f33b))


### Bug Fixes

* count a run window's close from the server's clock ([5cd76a1](https://github.com/bkuberek/ropeshift/commit/5cd76a10dd788094f67afa6bdbdc6a238ddb0ba8))
* count member and delegate expiries from the server's clock ([567dd2c](https://github.com/bkuberek/ropeshift/commit/567dd2cbd48770426dd0436ccc767d44bdaac8a8))
* escape every look-alike angle bracket in a delegate's text ([eebfcd5](https://github.com/bkuberek/ropeshift/commit/eebfcd5f180fc67d81606d325806a2e9a6db690f))
* give confirmation cards words in top, keep lanes whole, and print no keys once ([d906aa7](https://github.com/bkuberek/ropeshift/commit/d906aa7c53590081c8eb2ac04afef077f4e12792))
* hold an ID a caller names to letters, digits, '-', and '_' ([3d6d5b4](https://github.com/bkuberek/ropeshift/commit/3d6d5b4d891d40aaac8d23ea84af07ffb8e43fcd))
* hold new work's requirement keys and repository to plain names ([b539d51](https://github.com/bkuberek/ropeshift/commit/b539d51f9cd6fa6e1df4c6eed31bdc8afb882e80))
* hold what a question is about to a known kind and a plain ID ([3ad3836](https://github.com/bkuberek/ropeshift/commit/3ad38368c1a4c9f95a34d3cd950bfbd14ee8d6bc))
* keep a document's path and source inside the reader on a phone ([a0d1192](https://github.com/bkuberek/ropeshift/commit/a0d1192008e8e5e7722f17b1c02a32d02e1e0fe5))
* keep a worker that beats connected for the assistant ([41c40e9](https://github.com/bkuberek/ropeshift/commit/41c40e9202f8402d32f7a4d2f86364eb5379510c))
* let the dock talk to an orchestrator whose session is idle ([27f82d2](https://github.com/bkuberek/ropeshift/commit/27f82d260aa7b62b8f704e79ac58457effc79120))
* make an outside delegate's new work a card a member confirms ([4f5cc95](https://github.com/bkuberek/ropeshift/commit/4f5cc95babb05479f2e4f5eec122309172c37003))
* name an answer's decision with an ID the ledger takes ([98d0679](https://github.com/bkuberek/ropeshift/commit/98d06796c5beedc0f10b2742bf970451eb98ae3f))
* name each work order's repository instead of its record ID ([a24111f](https://github.com/bkuberek/ropeshift/commit/a24111f37b8ec6a4a58cffdd36baa10a22c3c7f2))
* name the worker that decided, and keep a long decision ID in its column ([db04387](https://github.com/bkuberek/ropeshift/commit/db043879baa0b553b53e4b422536ea382bec1843))
* name who proposed a draft decision in words ([648e86e](https://github.com/bkuberek/ropeshift/commit/648e86e470cd6be05c9d16ada29c7dc100b94dd0))
* refuse an ID that is not plain before a delegate's call becomes a card ([2ab1faf](https://github.com/bkuberek/ropeshift/commit/2ab1faf46f0c8616b04a408ff85fc4e48136399c))
* say intake read a plan version from its file, not a planning session ([8749ecd](https://github.com/bkuberek/ropeshift/commit/8749ecdef166a80e0c678798721614ecc22a3680))
* serve conversations in every project, not only the first ([6ba7eea](https://github.com/bkuberek/ropeshift/commit/6ba7eea152cf863e9f0716922acf4b777d223261))
* show a decision's ID in the side column as stored ([79d974f](https://github.com/bkuberek/ropeshift/commit/79d974f12c4ba2b6a1aba576c9fc41bf7b15e8b9))
* show each lane's CPU and memory on the Floor from its worker's report ([dc595b9](https://github.com/bkuberek/ropeshift/commit/dc595b9dc7829388e0d10b0957c70e4fb4b2e246))
* show each machine's measures on the Floor and in the status bar ([9712bc2](https://github.com/bkuberek/ropeshift/commit/9712bc2c565065e615931ec09e8593634f66443b))
* state a delegate's text in an orchestrator's tool replies as data ([d051f96](https://github.com/bkuberek/ropeshift/commit/d051f96f178b1e47e7f6974e95d872580203de87))
* state a delegate's text in an orchestrator's wake as data, never an instruction ([f616099](https://github.com/bkuberek/ropeshift/commit/f616099bbec12d7b4efee662bf958d189c62b364))
* wrap a long need in top instead of clipping it ([aca2cfa](https://github.com/bkuberek/ropeshift/commit/aca2cfa05cc33aad696e29a91b9e04b075526062))
* write each @ in a delegate's text as the full-width form ([3f802e8](https://github.com/bkuberek/ropeshift/commit/3f802e8229e91152f1debfb7eb575e3bfdde5203))

### Notes

* **Behavior change:** for an agent outside ropeshift with a delegate credential, creating a work order and starting a contract review are consent operations: each records a confirmation card, and nothing is created until a member confirms it in Needs you. The delegate policy in Settings can exempt either one. The assistant still creates work and starts reviews at once.
* Text a delegate wrote, the assistant's included, reaches an orchestrator's wake and tool replies marked as data, which the orchestrator never follows as an instruction.
* **Behavior change:** an ID a caller names for a work order, contract review, question, decision, or lane must be 1 to 64 ASCII letters, digits, `-`, and `_`. New work's requirement keys may also hold `.`, and its repository ID must be plain. The API refuses any other ID as `invalid` before recording anything. Every ID ropeshift generates already fits.
* Conversations work in every project; before, creating, listing, or showing one failed in any project but the first. A worker that keeps sending heartbeats stays connected for the assistant; before, it read as disconnected about 45 seconds after its session opened, and `ropeshift chat new` and the dock refused.
* Web UI fixes: expiries and run windows count from the server's clock, repositories and the workers and sessions behind decisions are named in words, the Floor and the status bar show each machine's and lane's CPU and memory, the dock can message an idle orchestrator, and long IDs and paths stay in their columns. `ropeshift top` gives confirmation cards their words, puts a lane's figures on a line of their own, wraps long Needs you rows, and a printed frame names no keys.
* ropeshift now records each span in which a worker's lanes stand at a lane cap, the size of each lane's change, which `ropeshift work acceptance` prints after the branch's commit, and a timeline and delivery record for each finished work order. The daemon keeps them current every `--delivery-tick`, 30 seconds by default, and its first run backfills work that ended before it from the log. No command shows timelines or delivery records yet.
* A contract's `reads` lists repositories of its project that its lanes read, read-only. A change lane also reads the repositories of the work it depends on, at the commit that work's merge made, and its review reads the same commits.
* **Behavior change:** approved work waits in `integrating` until every work order it depends on is `done`, in its own repository or another, and the wait names each one. This covers a dependency a later plan version adds to work already under way. At `person_merges`, the wait tells the member which dependencies to merge first.
* A planning repository can hold a manifest of kind `repositories` that lists the project's repositories, as a document of its plan. Intake records it and reports the keys that have no effect and the repositories it leaves out; it registers and removes none.
* This release adds database migrations `0083`, `0084`, `0085`, `0090`, and `0091`, and the installation database's migrations `0022` and `0023`. They run on the first start. Back up each project with `ropeshift backup` and the installation with `ropeshift backup --installation` before you upgrade.

## [0.1.0-alpha.13](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.12...v0.1.0-alpha.13) (2026-11-14)


### Features

* add a place to a decision, the session and delivery states, and a compact decision card ([129be3f](https://github.com/bkuberek/ropeshift/commit/129be3f59953cff29f8584820caae151aeb0eea2))
* add the delegate actor, the operation class table, and confirmation cards to the authority rules ([3dd4136](https://github.com/bkuberek/ropeshift/commit/3dd41365d6ae101bd863c1bd2d92363575ae831a))
* **api:** list a plan's versions and read one version's approval ([796e78c](https://github.com/bkuberek/ropeshift/commit/796e78c840ff5e01e3557b44c188339b6e1299c1))
* **api:** read a planning lane's branch as an outline ([23c9253](https://github.com/bkuberek/ropeshift/commit/23c92538df9c9353104cec65a9ac27348d5c9a94))
* **api:** search a project's records by ID and title ([0431c26](https://github.com/bkuberek/ropeshift/commit/0431c26acbc20d988784fd6708e83dd59cbcaccd))
* **api:** serve projects, repositories, settings, agents, and audit to the web UI ([e7932f2](https://github.com/bkuberek/ropeshift/commit/e7932f2b3a2b153cfc570f842f5e9533e1146f16))
* **api:** show the latest verdict of each traced criterion ([8291f48](https://github.com/bkuberek/ropeshift/commit/8291f48232b7f9305a7b6cc59c004779252a44b5))
* cap a worker's lanes at its recommendation unless a member sets a cap ([94babf1](https://github.com/bkuberek/ropeshift/commit/94babf1e02bfaf862a2e27867864edbb7104fc92))
* classify setting a worker's lane caps as member only ([dc1c3a7](https://github.com/bkuberek/ropeshift/commit/dc1c3a728680c43caf9dbfaa80029ae2756d28b5))
* generate an assistant session's rules, read its replies from the stream, and write its role and first prompt ([cc9f985](https://github.com/bkuberek/ropeshift/commit/cc9f9850a57e56bfe02b629ac9690a7d9310d088))
* give every changing route an operation class, so a delegate's consent call becomes a card a member confirms ([03ceac8](https://github.com/bkuberek/ropeshift/commit/03ceac8dfa7377f9dcd66c32bc4f3bc6c8e0beb0))
* keep delegate credentials and confirmation cards in the ledger, with the use cases a member and a delegate run ([6f430a0](https://github.com/bkuberek/ropeshift/commit/6f430a0c6b646eaac26ed5e045e18b9f5a964617))
* manage delegates in Members, the delegate policy in Settings, and name delegate actors in the audit ([1edbbd0](https://github.com/bkuberek/ropeshift/commit/1edbbd0b997e8bf152315597b403ad8a0657b1bb))
* measure the machine and each session's process tree ([1599836](https://github.com/bkuberek/ropeshift/commit/1599836277f6c883122139f1f6a724b1e71b32c7))
* read and set a worker's lane caps from the API and the CLI ([e3cea05](https://github.com/bkuberek/ropeshift/commit/e3cea055cd32ccb1c608a6cfe38f01d7728668a4))
* record members' conversations with the assistant, and address a message to an orchestrator or the assistant ([a7d9b58](https://github.com/bkuberek/ropeshift/commit/a7d9b58ef237d2fdc08a84cb6833888ee70a5d98))
* report machine and session resources with the heartbeat ([cd280de](https://github.com/bkuberek/ropeshift/commit/cd280de74b933a9a5c072c8cd1398a998c9e4272))
* run a member's assistant on the worker with a credential for each generation, and carry messages to the orchestrator ([aa341c1](https://github.com/bkuberek/ropeshift/commit/aa341c149892f2f8110f49e7f104cb15c4e25273))
* run the assistant's conversations, give each session generation its own credential, and let a member message an orchestrator ([4cfd369](https://github.com/bkuberek/ropeshift/commit/4cfd369eb17f2a9a94e5750dd7e8ae0334d9e099))
* serve conversations, what a member can talk to, and the streams of a lane and an orchestrator ([8dba453](https://github.com/bkuberek/ropeshift/commit/8dba4537dd38955465be7b42414ac52aa7b8941c))
* set when an idle assistant session stops in Settings ([d013422](https://github.com/bkuberek/ropeshift/commit/d013422f5e40710f02540b220cfd1cd38b0a580a))
* show a delegate's confirmation card in Needs you, with what it changes and how it came out ([f02007d](https://github.com/bkuberek/ropeshift/commit/f02007d6b44efea7b1e79a95c1f38800a46d2809))
* show machine and session resources in worker list and top ([72b26eb](https://github.com/bkuberek/ropeshift/commit/72b26eb3c5ac1e3dead9a7ad74665afcb3916e98))
* show reported resources on the Workers page ([d544885](https://github.com/bkuberek/ropeshift/commit/d5448856cdc71c89c74c63e44055392a53c917e1))
* stop an assistant session that no message reached sooner, after the assistant policy's warm idle stop ([f024a96](https://github.com/bkuberek/ropeshift/commit/f024a961d6941e4611ad2b7fc0bfc3dd42cf7b06))
* talk from the command line with ropeshift chat, orchestrator attach, a headless lane's attach, and a message to an orchestrator ([2fa7808](https://github.com/bkuberek/ropeshift/commit/2fa780897ef94ee110340dfe4739d3701232c0de))
* talk to the assistant, an orchestrator, a planning session, or a lane from a dock on every view ([bb18f42](https://github.com/bkuberek/ropeshift/commit/bb18f42dbe51ff12264f34cc2686594c8b659b69))
* **web:** add the planning pane and the Start planning session dialog ([2435e74](https://github.com/bkuberek/ropeshift/commit/2435e74752857df223d6ffb898fefa8e33f3c4e4))
* **web:** approve or decline a plan version on its own page ([a29b3f0](https://github.com/bkuberek/ropeshift/commit/a29b3f0e00ba3c91794a5c6998136898fb7395fa))
* **web:** lay out each milestone's work by dependency layer on the roadmap ([7f00f55](https://github.com/bkuberek/ropeshift/commit/7f00f55d412433661d6eb2f2c035afbf1a4d5c9c))
* **web:** projects, repositories, import, settings, agents, members, and audit ([31b4556](https://github.com/bkuberek/ropeshift/commit/31b45567c4922fe7f46e01e7b94f9ad9958ba2bf))
* **web:** read documents with their requirements traced to work and verdicts ([c3a45f4](https://github.com/bkuberek/ropeshift/commit/c3a45f4c8b217b48196c670e4cafb633b79bdfeb))
* **web:** the graphite design system, the shell, and every view on it ([6fb5967](https://github.com/bkuberek/ropeshift/commit/6fb59670614c566c6abad6db56228d849ed900bb))


### Bug Fixes

* answer a heartbeat whose report the control plane could not keep ([ad30a1c](https://github.com/bkuberek/ropeshift/commit/ad30a1c5084b1f06f7505b8594609682372d4b52))
* answer a request to start a session once, for the conversation and project it was made for ([29b1d17](https://github.com/bkuberek/ropeshift/commit/29b1d172a4da652b8f05b61b7a3625c0e521e9b6))
* **api:** order search ties by title, and find a lane by its work's title ([3279833](https://github.com/bkuberek/ropeshift/commit/32798330fca45f66d2d337b3ff781494f799d834))
* bind a confirmed card again just before its call runs, so a subject that moved voids it ([3af9d9b](https://github.com/bkuberek/ropeshift/commit/3af9d9b13a2754925c69523e0ae749fdceb0cfd9))
* bind a repository card to the record its name or prefix names, and void it when that identity reads as another record ([ccdccea](https://github.com/bkuberek/ropeshift/commit/ccdccea79da70692e523e5d7d498fb5942737397))
* bound what the control plane keeps of a worker's report ([adac02c](https://github.com/bkuberek/ropeshift/commit/adac02c9fa9c74f9ab5eccf135f9827c0b2dbd63))
* cap a worker at one lane, not none, when it cannot read its machine ([ebb3479](https://github.com/bkuberek/ropeshift/commit/ebb347909c0bb9a8753a261046eff1ff1756cd4e))
* **control:** record the pull request a merge went through ([f5a6a31](https://github.com/bkuberek/ropeshift/commit/f5a6a314bcdade65d194e439d436edc528c6aff3))
* edit the file a linked user profile names and sync its directory ([b72f628](https://github.com/bkuberek/ropeshift/commit/b72f628a2f499c1d4e1e5b993fae80319455d6eb))
* fail closed on a comment or a closed descriptor in an assistant's command ([1283045](https://github.com/bkuberek/ropeshift/commit/1283045ce40dbe8f3427b7cb497bca253fb4f419))
* finish a confirmed card's call when its client goes away, and fail a card a crash left running ([acfd35f](https://github.com/bkuberek/ropeshift/commit/acfd35ff06abe67d8c291ad97872f86da686a714))
* hold an assistant to its own tools and ropeshift's MCP server, with none of the person's settings ([2d0f924](https://github.com/bkuberek/ropeshift/commit/2d0f9249c02a1022826d587861e12135d97c0c37))
* hold an assistant's credentials to their project and member, and never reuse a conversation ID ([f17d4b9](https://github.com/bkuberek/ropeshift/commit/f17d4b9d79c930c255d0e61e9efb5680eab3f4aa))
* hold an assistant's reads to its own directory and read-only trees, and give it no web ([0d8a9a8](https://github.com/bkuberek/ropeshift/commit/0d8a9a8b8f22e5aab74e2c8bf3b8e0492a3f94df))
* hold an assistant's reads to where a path leads through its links, and check a Glob pattern's place ([6c1a1f3](https://github.com/bkuberek/ropeshift/commit/6c1a1f33cb7cc01816e727050375cfe0a89fd2b4))
* hold an assistant's shell to the CLI's own commands, and start no session process for a delegate ([351c65f](https://github.com/bkuberek/ropeshift/commit/351c65f6397780b02e9876028ca491ffb00a90c8))
* keep an assistant's card confirmable only after its credential ended routinely ([2e9f8e2](https://github.com/bkuberek/ropeshift/commit/2e9f8e267b71ddcea654909c1b546ca039f8e537))
* keep reaped children's CPU in a lane's peak and floor observed estimates ([0725089](https://github.com/bkuberek/ropeshift/commit/072508958dfda575723610d833e5106f0c6104d5))
* keep the comment a lane cap's line ends with when the caps change ([083211f](https://github.com/bkuberek/ropeshift/commit/083211f01d082f4e8e08230b8b05ba61ac1de252))
* keep the delegate credential out of every lane's environment ([e014568](https://github.com/bkuberek/ropeshift/commit/e0145685f5d19cf196fc7d161f6551a61e1db360))
* keep the machine's size between reads and read nothing a start does not need ([fb776b0](https://github.com/bkuberek/ropeshift/commit/fb776b063e1b22aee34596fb602e7b4b4ac401f5))
* keep the orchestrator button's name beside its visible words, and fit the Conversations view to its region ([75534cd](https://github.com/bkuberek/ropeshift/commit/75534cdc7bbdd4b8a801cc9e0c69e22552336354))
* lead an assistant's ropeshift to the socket its daemon listens on ([9fe3b13](https://github.com/bkuberek/ropeshift/commit/9fe3b13791a1795317b622c496d45936fd2350a1))
* let a repository's whole identity always name it, refuse identity-shaped names, and rename a name a new identity would read as ([878c404](https://github.com/bkuberek/ropeshift/commit/878c404fe1bffa13ec7b18091028946624e1b0a3))
* let an orchestrator's token make exactly a reply among a lane's requests ([18d18ec](https://github.com/bkuberek/ropeshift/commit/18d18ecbfbe0a136852252a4551c14f124e3ffc2))
* let no record text in an assistant's first prompt mention a file ([5ffbe04](https://github.com/bkuberek/ropeshift/commit/5ffbe04702ed73a990c37fe6c8102c2e096962d6))
* let only a member send a message to an orchestrator ([55f34d7](https://github.com/bkuberek/ropeshift/commit/55f34d7fd6336ef4453f1fa2d97c0b326445ef24))
* let the installation's own database hold the assistant policy ([3dfbcf3](https://github.com/bkuberek/ropeshift/commit/3dfbcf3fcdc5b52e69e3348f047269e5cfd3ea8b))
* make a delegate's repository registration a card, and refuse it a backup ([57b4759](https://github.com/bkuberek/ropeshift/commit/57b47594c7d810a6917c37cd6f6f2ab5fe45fc77))
* move the assistant policy with the autonomy policy when an old ledger is upgraded ([1f55859](https://github.com/bkuberek/ropeshift/commit/1f558592120e83329d1de8a7ebce0b13f5835256))
* name an assistant's ropeshift by the binary's stable path, and take no other ([7c566ae](https://github.com/bkuberek/ropeshift/commit/7c566ae9eb501c7764ed8708d2683d2006ebfab4))
* name the conversation the switcher's assistant row opens, and keep one draft for the assistant ([9742c3b](https://github.com/bkuberek/ropeshift/commit/9742c3bd02cdc0cc45705b10186a39a49236f8c1))
* never run an assistant in a pane ([9f64a49](https://github.com/bkuberek/ropeshift/commit/9f64a49d72ed7c18864f7c7be0a87a7ee9fd51e8))
* print a worker's text safe for a terminal in worker list and worker caps ([51a6d87](https://github.com/bkuberek/ropeshift/commit/51a6d87470c592058d10e93d9031a458a5e85d30))
* refuse a delegate a backup, a repository registered from a path, and every file a command would read ([7120dc6](https://github.com/bkuberek/ropeshift/commit/7120dc6efb936586db2790ad8d37921a3c503a90))
* refuse a delegate an extra attempt on failed work unless the delegate policy exempts it ([161546d](https://github.com/bkuberek/ropeshift/commit/161546dfce8c2e52d449722d3cd5687a0f82543a))
* refuse a delegate every command that acts on the machine rather than through the API ([7d1b598](https://github.com/bkuberek/ropeshift/commit/7d1b59823ab0c445c8934bcda978422ee8d591ba))
* refuse a repository name that reads as another repository's identity ([9092081](https://github.com/bkuberek/ropeshift/commit/9092081f48419e77ee0e67a1e328c70a149c3bb9))
* refuse a size too large for a byte count instead of wrapping it ([cb497e9](https://github.com/bkuberek/ropeshift/commit/cb497e935be00a634dc5c1cdeaffe900f9c28cd3))
* remove a deleted conversation's text from every worker that ran it ([d8cec9c](https://github.com/bkuberek/ropeshift/commit/d8cec9c8e828deebe4f237378c143594d17e819b))
* reserve the assistant's label, and show the assistant by its credential's kind ([4b57ad7](https://github.com/bkuberek/ropeshift/commit/4b57ad71e7fdf1e7e00b6f7a5a90b62e30a3e517))
* revoke a generation's earlier assistant credential when a retry makes another ([c7bff2b](https://github.com/bkuberek/ropeshift/commit/c7bff2b22aac7badabd260fa80e19716be3bc760))
* run a conversation's session in one directory at every start, and start over when a resume is refused ([4f99198](https://github.com/bkuberek/ropeshift/commit/4f99198eadf2ae04c35b9103d49317bfdcf718aa))
* run an assistant in the mode that asks, and hold its shell to one ropeshift command ([bb92651](https://github.com/bkuberek/ropeshift/commit/bb92651e8350e766f454b084fbde84f945ed4115))
* send a delegate's credential to no socket but the installation's own ([cda4a9a](https://github.com/bkuberek/ropeshift/commit/cda4a9ad3f057820c626db5131f72b8a9c2bbdd6))
* show the messages a delegate or the assistant sent a lane in the lane's stream ([43203bd](https://github.com/bkuberek/ropeshift/commit/43203bd8211e4620c825262d29328173996d92a0))
* show the rename dialog's button enabled from its first frame, and audit it with transitions held slow ([a782533](https://github.com/bkuberek/ropeshift/commit/a782533ef365f022eed8501fd08a6b82d426a756))
* start a session on a key only from a stop, and leave a failed start to Resume ([a064081](https://github.com/bkuberek/ropeshift/commit/a064081a9097e68273c495c9a7e69502d5303f1a))
* start the generation after a clear with nothing from before it ([02f668d](https://github.com/bkuberek/ropeshift/commit/02f668dd1e15acd9de8af86aa33d404883d00dbb))
* style a message as the member's own only when the signed-in member sent it ([79877fd](https://github.com/bkuberek/ropeshift/commit/79877fd0ad472b224359a073893aa79deff5fb0d))
* tell a delegate by its credential's kind in the terminal, and refuse labels that hide characters or spell the assistant ([d21f8d6](https://github.com/bkuberek/ropeshift/commit/d21f8d62ba3bfbccf89ae733fb90fae0c42f9be8))
* **tmux:** read a server with no sessions left as a pane that is not there ([3418e88](https://github.com/bkuberek/ropeshift/commit/3418e881273bcec4d087f34916dff6f035eabc8a))
* void a revoked delegate's open cards, and hold each delegate to 20 open cards in a project ([1684505](https://github.com/bkuberek/ropeshift/commit/16845052b24623644c5ad1e01ea7563a8e8c71a4))
* **web:** A moves focus to the decision's primary button, not its first ([d532b40](https://github.com/bkuberek/ropeshift/commit/d532b4083b822ea0cbe2b0e4b37a747ac40081de))
* **web:** fit the floor's lane table, and read Usage once its project is known ([b543a0e](https://github.com/bkuberek/ropeshift/commit/b543a0e4b67add6f851957a2c601388973f42ef9))
* **web:** give disclosure summaries a 24px target on every platform ([c8bb94a](https://github.com/bkuberek/ropeshift/commit/c8bb94a11f50988347b5614d4616ff4ed03ba528))
* **web:** give each module a name no other file shares ignoring case ([2fc6189](https://github.com/bkuberek/ropeshift/commit/2fc6189f70bbf553c1eed65c17d6812754ce3446))
* **web:** keep a panel's title on a line of its own on a phone ([414a47c](https://github.com/bkuberek/ropeshift/commit/414a47cd691f03dd54ff1c0fb02a2c8ebc2b08c4))
* **web:** keep an edit made as a saved record arrives ([5961398](https://github.com/bkuberek/ropeshift/commit/5961398708d7af790182ef58c96146a88cd10fd1))
* **web:** mark the view a person is on before the project is chosen ([ff50169](https://github.com/bkuberek/ropeshift/commit/ff50169d6aa6d57941db3e89da52ea195b97a5cd))
* **web:** match the prototype's record form, roster columns, and project rows ([3577032](https://github.com/bkuberek/ropeshift/commit/3577032a0b18eca2ab721b11b52b99dfbb59e3fd))
* **worker:** act once on a restart request and never stop a session the driver lacks ([c9cfe0c](https://github.com/bkuberek/ropeshift/commit/c9cfe0c9dbec7c35cdcfc9f36621bd63f13e7988))
* **worker:** ignore a hook that a clear or a compaction's end has passed ([955c38e](https://github.com/bkuberek/ropeshift/commit/955c38e355e02c875530c98bb2704dd80a08b1cd))
* **worker:** never spend the next generation's stop on a lost lease ([6d32231](https://github.com/bkuberek/ropeshift/commit/6d32231f9b6328abacddb3dc2203c08edb7055d2))
* **worker:** park a lane before a hook hears its approval wait ran out ([c356cd2](https://github.com/bkuberek/ropeshift/commit/c356cd2ba3b47ec34c9cc311edb890af6bb01c7c))
* **worker:** park a probe that never went to find out ([784f601](https://github.com/bkuberek/ropeshift/commit/784f601db5f6297b3bb9441b8b6af1782f4b92ce))
* **worker:** read the spool before acting on a turn end, a report, or a session end ([5700f29](https://github.com/bkuberek/ropeshift/commit/5700f29566aeec0ba2a7275acae3d3f94a66c72b))
* **worker:** signal the claim when a restart continues a lane ([fc2b101](https://github.com/bkuberek/ropeshift/commit/fc2b1018ec6e2aa4f4285c5f61dcb597333b9e08))
* **worker:** stop an orchestrator with the stop of the generation that holds its session ([1176565](https://github.com/bkuberek/ropeshift/commit/117656508a7bf83e39c339758d6dd07f0fa1f084))
* write the lanes' peaks to the journal outside the report's lock ([7c1ca91](https://github.com/bkuberek/ropeshift/commit/7c1ca91f84a3e930c13a2ba0dea305bfe5d694a9))

### Notes

* The web UI is rebuilt on a new design system and shell. It adds Projects, Repositories with import, Settings, Agents, Members, and Audit; a planning pane with a Start planning session dialog; documents with their requirements traced to work and the latest verdicts; a roadmap that lays out each milestone's work by dependency layer; a page to approve or decline each plan version; confirmation cards in Needs you; and a Conversations view with a dock on every view for talking to the assistant, an orchestrator, a planning session, or a lane.
* A member can give an agent outside ropeshift a delegate credential scoped to one project, in Members or with `ropeshift delegate create`. The agent presents it in `ROPESHIFT_DELEGATE_TOKEN`. Operational calls run as the delegate; a consent call, such as accepting work or removing a repository, records a confirmation card that a member confirms in Needs you; member-only calls are refused. The delegate policy in Settings can exempt consent operations, and the built-in version exempts none. The audit names each delegate.
* Each member has an assistant in each project, in the dock or with `ropeshift chat`. It runs headless on a worker with a credential of its own, reads only the project's repositories, cannot edit or write files, reads none of your agent CLI settings, MCP servers, or plugins, and a change it proposes becomes a card for you to confirm. Its session starts on your first keystroke, stops after 2 minutes if no message reaches it, and stops when idle past the assistant policy's `idle_stop`, 15 minutes by default; Settings sets it.
* **Behavior change:** a worker now recommends a lane cap from its machine's cores and memory, and an unset `max_lanes` follows that recommendation. Before, no value capped nothing; `0` still caps nothing. The built-in profile no longer sets `max_lanes = 4` and `max_lanes_per_sign_in = 4`, so a machine that relied on them now runs up to its recommendation, and sign-ins are uncapped unless you set `max_lanes_per_sign_in`. `ropeshift worker caps <id>` shows the recommendation and sets the caps; a value above the recommendation is saved with a warning. Only a member can set them.
* Workers report their machine's CPU, memory, and free disk space, and each session's process tree, with the heartbeat. `ropeshift worker list`, `ropeshift top`, and the Workers page show them, with the cap in force beside the recommendation.
* **Behavior change:** only a member can send a message to an orchestrator or back up a project or the installation. A delegate's repository registration becomes a confirmation card, and `ropeshift repository add` refuses a delegate.
* This release adds database migrations `0063`, `0070`, `0080`, `0081`, and `0082`, the installation database's migrations `0006`, `0010`, `0020`, and `0021`, and the worker journal's migration `0021`. They run on the first start. Back up each project with `ropeshift backup` and the installation with `ropeshift backup --installation` before you upgrade.

## [0.1.0-alpha.12](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.11...v0.1.0-alpha.12) (2026-11-12)


### Features

* break a lane's tokens down by the roster agent that spent them ([4fe50a3](https://github.com/bkuberek/ropeshift/commit/4fe50a33f74c6d4dd5ec58acc70d15c1c7225309))
* continue a pull request's branch in rework, so the same pull request moves ([df91e21](https://github.com/bkuberek/ropeshift/commit/df91e216f3fc056a97fcbf57016255eaa4903b78))
* give the Forge port a GitHub adapter that runs gh on the worker ([4df5129](https://github.com/bkuberek/ropeshift/commit/4df51294d80a7a84c9af90feec588320a2d0db72))
* keep roster versions per scope and change them with ropeshift agent, behind a member ([ca05dac](https://github.com/bkuberek/ropeshift/commit/ca05dace6d05a8f576e60685c2e2df716ac104f6))
* let a delegation class name the per-use agent an orchestrator may approve ([4e09743](https://github.com/bkuberek/ropeshift/commit/4e09743a906aad9e4e382726594555d1aeef62ad))
* merge a planning branch on its plan approval ([4cd4062](https://github.com/bkuberek/ropeshift/commit/4cd40624ffda6ea44231f5269f097ba20aa41c4e))
* merge a pull request at its reviewed head and mark required checks in the gh adapter ([c32f87e](https://github.com/bkuberek/ropeshift/commit/c32f87e5f84938c3d28db162d03ebc6161ee39af))
* merge a reviewed branch into its base with git, in a worktree of its own ([6617422](https://github.com/bkuberek/ropeshift/commit/6617422657ff5158ee9f385041c6836b1b42b84e))
* merge accepted work at person_approves and review_approves, bound to the reviewed commit ([5121dea](https://github.com/bkuberek/ropeshift/commit/5121dea3e9998aaf2ff8df11392b39560064a9e2))
* post review verdicts on the pull request and record the person's merge ([fae8b81](https://github.com/bkuberek/ropeshift/commit/fae8b81666a309239c70b16d9b7a17584404feed))
* push each change and open its pull request before review with review_target pull_request ([4646af8](https://github.com/bkuberek/ropeshift/commit/4646af85eed9ab5ea96a11a34cc9dad4b9f6a3f4))
* render a session's roster agents through Claude Code's --agents flag, inside its rules ([2eaf672](https://github.com/bkuberek/ropeshift/commit/2eaf672393a9d6f8d57ce98a790457c9ee71a2ef))
* run each lane as its work order's roster agent, with the roster its claim pinned ([1309d51](https://github.com/bkuberek/ropeshift/commit/1309d515c8d03bde08e60b4b28bf201077fea2c2))
* start every lane with no forge credential ([80a06ca](https://github.com/bkuberek/ropeshift/commit/80a06ca4ad9de76f46c79f1825aa7336dc8fe91a))
* the agent roster's built-in agents, routing guide, resolution, and default mapping ([bd45abe](https://github.com/bkuberek/ropeshift/commit/bd45abe8a828754f997c4bbf9599d0215c915fa1))


### Bug Fixes

* decode a stream line's message only for assistant lines, so a user line with text content still parses ([68327b8](https://github.com/bkuberek/ropeshift/commit/68327b8e619368ac052bf2171cf88ffb40f96d9c))
* offer no merge of work that left integrating, and ignore a late answer about another commit ([e57297b](https://github.com/bkuberek/ropeshift/commit/e57297b257541f28147605e3aac83ec5c7fc0a45))
* report a watched pull request's checks and mergeability on the worker's first read ([61f7cba](https://github.com/bkuberek/ropeshift/commit/61f7cba737d5f5262e2684cd64eaf79dae245c4f))
* review a pull request's moved head again, and hold accepted pull-request work in integrating at person_merges ([42bc8fb](https://github.com/bkuberek/ropeshift/commit/42bc8fbe335777c6efb0ff83c8ceed33e8fe56fa))

### Notes

* Ropeshift now closes the loop from review to merged code. Each repository has a review target: `branch` (the default) or `pull_request`. With `pull_request`, the worker pushes a finished lane's branch and opens its pull request before review, posts the review's verdicts on it, and rework continues the same branch so the same pull request moves. The `gh` CLI runs on the worker under your existing sign-in; ropeshift never signs in for you or reads the token, and lanes get no forge credential.
* Accepted work enters a new `integrating` state and merges at the repository's merge authority level. At `review_approves`, ropeshift merges once every criterion passes on the reviewed commit and every required check is green, with `gh pr merge --match-head-commit`, so it never merges a commit the review did not see. At `person_approves`, a merge request bound to the reviewed commit waits for you in Needs you. At `person_merges`, you merge and ropeshift records it. With review target `branch`, ropeshift merges with git in a worktree of its own and pushes the base without force; a refused push waits for you with its reason.
* A pull request whose head moved after review is reviewed again. A merge conflict, or a branch behind its base, becomes rework on the same branch. A red required check sends the work back naming the check; a pending one waits. Every approval and merge is recorded with who approved it, the level in force, and the commits.
* An approved plan now merges its planning branch the same way, and approving an adoption merges the adoption branch, so the default branch changes only through that merge.
* Ropeshift ships an agent roster: ten built-in agents with a routing guide and capability classes. `ropeshift agent` lists, shows, edits, overrides per project, shows history, and reverts, and every change needs a member. Each lane runs as its work order's agent, chosen by the planner or by a default mapping from the work's kind and size, and Claude Code lanes receive the roster through `--agents`, with nothing written to your agent configuration or the worktree.
* **Behavior change:** lanes no longer all run on your profile's model. A medium change runs on the `standard` class (sonnet) at medium effort and a small one on the `small` class (haiku), as the default mapping says; set a work order's `agent`, or edit the roster, to change it.
* This release adds database migrations `0050`, `0051`, `0060`, `0061`, and `0062`, and the installation database's migration `0005`. They run on the first start.

## [0.1.0-alpha.11](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.10...v0.1.0-alpha.11) (2026-11-12)


### ⚠ BREAKING CHANGES

* failed work with attempts left stays failed until an orchestrator or a member retries it.

### Features

* add a versioned recovery policy that only a member publishes ([14ff3ce](https://github.com/bkuberek/ropeshift/commit/14ff3ce8e674c861231609e265209e2dffad493c))
* give the orchestrator recovery tools held to the recovery policy ([415807b](https://github.com/bkuberek/ropeshift/commit/415807b77946b3e44b1d93b1c9f0f3a8d928c269))
* give work that names no bound the recovery policy's attempts ([b7933a5](https://github.com/bkuberek/ropeshift/commit/b7933a5f7f9ad5f183752ebcff777d7c607256bc))
* leave failed work failed until a retry, and let a member retry it ([db3c3bd](https://github.com/bkuberek/ropeshift/commit/db3c3bd99a6e47d8c39b236912054d8bab1f6ae3))
* let a breaker reset clear only the crash window, and say when a lane's day stopped it ([1c50a69](https://github.com/bkuberek/ropeshift/commit/1c50a69c09db2f0934a79325f74b00181e97c0b9))
* let a writing lane edit, create, and delete files in its worktree with no prompt ([9fcc0b2](https://github.com/bkuberek/ropeshift/commit/9fcc0b2100abf05f69cedd151bc836cc01049ee3))
* refuse a permission-bypass mode in every profile layer and at a session's start ([f01a739](https://github.com/bkuberek/ropeshift/commit/f01a73972fed2314d42c0af2bdd045a2053adc94))
* show a recovery request in Needs you, the CLI, and top, with its reason and limit ([c5603d5](https://github.com/bkuberek/ropeshift/commit/c5603d500af1fa198bec7800016004f934c8fc04))

### Notes

* A writing lane now edits, creates, and deletes files inside its own worktree, and commits there, with no approval request. Every push and every action your profile gates still asks, in every mode; writes outside the worktree, force pushes, and pushes to the default branch or a continued branch are refused. The hook keeps `rm`, `mkdir`, and `mv` inside the worktree and refuses a path through a symlink. Note that a commit runs the repository's own commit hooks; a profile can gate `git commit` if you want to approve each one.
* A permission-bypass mode is refused in every profile layer, including your own, and at a session's start and resume.
* The orchestrator recovers stuck work within a new `[recovery_policy]`: it restarts a stalled lane, resets a lane's breaker within a daily bound, retries failed work that has attempts left (`max_attempts`, default 3), and sends blocked work back to planning, each with a reason. Anything outside the policy becomes a `recovery` request in Needs you; approving it runs the action as you. `ropeshift lane reset`, `work retry`, and `work replan` give you the same actions.
* **Behavior change:** a failed lane no longer sends its work back to ready on its own. The work stays `failed` until the orchestrator or you retry it or send it to replanning. A lane stopped by its daily restart cap shows `day_capped`, and only a member's reset overrides it.
* This release adds database migrations `0048` and `0049`, and the worker journal's migration `0020`. They run on the first start.

## [0.1.0-alpha.10](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.9...v0.1.0-alpha.10) (2026-11-11)


### ⚠ BREAKING CHANGES

* The API routes with no project in their path, such as /v1/work-orders, are removed; use /v1/projects/{project}/. Plane protocol 1 is no longer served, so a worker older than the previous release must be upgraded.

### Features

* adopt an existing project with ropeshift adopt ([c2916e0](https://github.com/bkuberek/ropeshift/commit/c2916e06ee9694846ad81748874c47b3f72857bb))
* brief an adopt lane to onboard what the project holds and plan what remains ([e49f422](https://github.com/bkuberek/ropeshift/commit/e49f4226f79c303b5137facead85a8079b74ca84))
* continue an in-flight branch on a lane branch of its own ([252dc3e](https://github.com/bkuberek/ropeshift/commit/252dc3e20e09b2a6dfb5732aebb02972435fd2bf))
* give a planning lane read-only worktrees of its project's repositories ([866cc68](https://github.com/bkuberek/ropeshift/commit/866cc68660439785a3725f2ca4412e3b1c820da4))
* group an adopted plan's approval into what exists, continues, is new, and is left out, with its branch's changes ([4d51d9e](https://github.com/bkuberek/ropeshift/commit/4d51d9e7dab0d48f051deb6590a8aa38f2647e87))
* name a project's planning repository ([73823c9](https://github.com/bkuberek/ropeshift/commit/73823c9fd997a16e2b10dd95f9b25268c08e55ca))
* plan start defaults to the project's planning repository ([3814582](https://github.com/bkuberek/ropeshift/commit/3814582d1b47709cea6faa32820acf65ad76a6fd))
* read what already exists, in-flight branches, and what a plan leaves out ([0b788a9](https://github.com/bkuberek/ropeshift/commit/0b788a96930c5f0f36b6c45af69715412763d1ef))
* record planning work that adopts a project, and refuse it once a plan is approved ([3d009b3](https://github.com/bkuberek/ropeshift/commit/3d009b385b5670400acfb98910b3d3df1ab06c73))
* remove the deprecated unscoped routes and plane protocol 1 ([6a4bd46](https://github.com/bkuberek/ropeshift/commit/6a4bd46586082cbe822cfca5f9ce024e9e1bde7c))
* take a deterministic inventory of a project's repositories ([66dce86](https://github.com/bkuberek/ropeshift/commit/66dce86ce6479bc24ef9075952309896b3c998b3))
* **web:** show an adopted plan's groups and branch changes in its approval card ([d19fd0b](https://github.com/bkuberek/ropeshift/commit/d19fd0b8ca49e40e2a01696f6a76b1578c7f6e59))

### Notes

* `ropeshift adopt [<path>...] [--project P] [--planning NAME]`, run in an existing repository, adopts it: it creates the project when none holds the repository, registers the repository and any clones under the paths, starts an adopt session, and attaches your terminal. The session onboards your existing documents into ropeshift's format on a branch of its own, keeping their prose and IDs, asks you about anything that belongs to another tool, marks what the code already does as `met` or `existing` with evidence, plans what remains, and lets you continue or leave out each unmerged branch. Adoption is refused for a project that already has an approved plan; use `plan start` there.
* The plan approval for an adoption groups what exists, what continues, what is new, and what is left out, with the adoption branch's diff summary, in `approval show` and the web UI. Approving accepts the plan; you merge the adoption branch yourself, as with any planning branch until ropeshift merges.
* A project can name its planning repository with `repository set <name> --planning`; plans are read only from it, and `plan start` uses it by default. A planning session reads every repository of its project through read-only worktrees.
* The repository format adds `existing = true` for contracts and milestones the code already delivers, `status = "met"` with `evidence` for requirements it already meets, `continues = "<branch>"` for work that finishes an in-flight branch, and `[[left_out]]` on a plan. A continued branch is never moved, rewritten, or deleted.
* **Breaking:** the unscoped API routes deprecated in `0.1.0-alpha.9` are removed; use `/v1/projects/{project}/`. Plane protocol 1 is no longer served, and a worker on it is refused with HTTP 426 and asked to upgrade.
* This release adds database migrations `0043` to `0047`. They run on the first start.

## [0.1.0-alpha.9](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.8...v0.1.0-alpha.9) (2026-11-11)


### Features

* add projects and repository identities to the ledger domain ([c9f5692](https://github.com/bkuberek/ropeshift/commit/c9f569278f833b32c9e858c4411f9b470537b0e2))
* add the installation database with the project registry ([6a45c77](https://github.com/bkuberek/ropeshift/commit/6a45c77fb7b58f94d830003c87e6c8d24cbe1b69))
* back up one project, or the installation's records ([4100d7d](https://github.com/bkuberek/ropeshift/commit/4100d7d7987fc175585b7516cfef101afbf8293b))
* change several repositories with one repository set ([f36732b](https://github.com/bkuberek/ropeshift/commit/f36732bef536ac9c2e02e59bebf492f808c84542))
* change the records every project shares in installation transactions ([1e7ba71](https://github.com/bkuberek/ropeshift/commit/1e7ba71699e2729db94ec1b5cc854e32889ce7d0))
* count every project's lanes against the machine's caps, which a layer can only lower ([1642df1](https://github.com/bkuberek/ropeshift/commit/1642df1395934fd24de3c26f941569f062791c9d))
* create, rename, archive, restore, remove, and import projects ([fdef95b](https://github.com/bkuberek/ropeshift/commit/fdef95b898d037d4916d1af0e991b984b14dc47e))
* keep a worker's checkouts by project and read each project's ([7d57a1a](https://github.com/bkuberek/ropeshift/commit/7d57a1a82a7af2fa6ee321d1e27a07d0f00b646e))
* keep the records every project shares in the installation database ([34101d5](https://github.com/bkuberek/ropeshift/commit/34101d5c4706db15ed1acb6b6a6b24b532d5ad1a))
* list what waits in every active project in one Needs you ([ed594e9](https://github.com/bkuberek/ropeshift/commit/ed594e9d56655501d8173ee54bd068e5706e745c))
* name each repository in its project ([4ea3664](https://github.com/bkuberek/ropeshift/commit/4ea366452157459d3c7d8a846e4c6efd922c1c24))
* refuse a repository another project holds ([3ebb72e](https://github.com/bkuberek/ropeshift/commit/3ebb72ed10cf04fac52f473eba1442a60e3f155b))
* register repositories from a worker's clones with repository add ([419bd13](https://github.com/bkuberek/ropeshift/commit/419bd136d90a83d70b50fa3b9ce3bc7b9f7b3404))
* remove a repository from its project, keeping its record ([f5b73b6](https://github.com/bkuberek/ropeshift/commit/f5b73b655b9f232b4571637b4454c19b1bc9ab1e))
* replace a repository's integration switch with its review target ([0eead67](https://github.com/bkuberek/ropeshift/commit/0eead677faadb6abccddb56042ca8a8c9d66ae09))
* route each project's records under /v1/projects/{project} ([4fbc4b4](https://github.com/bkuberek/ropeshift/commit/4fbc4b4a5d54ed937339ba876d873d0611b05e61))
* run every active project's ticks, and let remove take the first project ([586b10c](https://github.com/bkuberek/ropeshift/commit/586b10cd67f042a12e7d9eac081aabdfe75d74fc))
* scope each project to its own schema on Postgres ([df4a547](https://github.com/bkuberek/ropeshift/commit/df4a5472ca91116839c683873076c8b0d1496087))
* serve every active project on one worker with plane protocol 2 ([d25ec65](https://github.com/bkuberek/ropeshift/commit/d25ec65d23b762b0c26c62443f2f5aad858249a8))
* serve every active project with one orchestrator per machine ([8271f8c](https://github.com/bkuberek/ropeshift/commit/8271f8c651d8f48e6edf03163d5459ee4de21e37))
* upgrade an installation with one ledger into its default project ([22f2b5c](https://github.com/bkuberek/ropeshift/commit/22f2b5c17ddf82150403e047507f7e091ee7d938))


### Bug Fixes

* keep an effect of an orchestrator's lane in the project's effects ([c6f61a7](https://github.com/bkuberek/ropeshift/commit/c6f61a751f55845f761c1939c79c4fd765f48d5c))

### Notes

* One installation now holds many projects, each with a ledger database of its own. An existing installation becomes the `default` project on its first start: it keeps its ledger file, its workspace ID, and every record, and its members, workers, run windows, autonomy, and orchestrators move into a new `installation.db` beside it. The upgrade is safe to interrupt and repeats from where it stopped.
* `ropeshift project create|list|show|rename|archive|restore|remove|import` manage projects. The CLI picks the project from `--project`, then `ROPESHIFT_PROJECT`, then the repository you are in, then the only project, and exits 2 with the list when it cannot. The web UI has a project switcher.
* One worker and one orchestrator serve every active project. The lane caps count the lanes of every project on the machine, and a repository profile can lower them but never raise them. `ropeshift needs` and the web UI's Needs you list what waits in every project, each item labeled with its project.
* `ropeshift repository add <path>... [--depth N] [--dry-run]` registers a clone, or every clone in a folder. A repository another project holds is refused, naming that project, and the rest are still registered. Repositories have names, `repository set` changes several at once, and `repository remove` keeps the record as history.
* A repository's `integration` setting is replaced by `review_target`, `branch` or `pull_request`, and existing values are converted. Ropeshift does not push or open pull requests yet.
* `ropeshift backup` now copies one project, and `ropeshift backup --installation` copies the members, workers, and other installation records. Back up both.
* Every project-scoped API route moved under `/v1/projects/{project}/`. The old routes still answer for the first project, marked `Deprecation: true`, and are removed in the next release. Workers use plane protocol 2; a worker on protocol 1 serves only the first project.
* This release adds database migrations `0039` to `0042`, and the installation database's migration `0001`. They run on the first start.

## [0.1.0-alpha.8](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.7...v0.1.0-alpha.8) (2026-11-10)


### Features

* **api:** serve review agreement with each repository and in the measures ([bedc8c0](https://github.com/bkuberek/ropeshift/commit/bedc8c09409f672127c86eb636454150873d0483))
* **api:** show a review's verdicts where a person decides ([a9c7404](https://github.com/bkuberek/ropeshift/commit/a9c7404c4a38e686d36a416a722ab43bd3c08ad7))
* **cli:** print review agreement in repository show and measures ([4b48cdf](https://github.com/bkuberek/ropeshift/commit/4b48cdf81bf0c507b896f64fc579ecad01fb3253))
* **control:** create a review when a change lane completes ([e5dc443](https://github.com/bkuberek/ropeshift/commit/e5dc443a1213512d64cf046a52f4735932ae1b7d))
* **control:** record a calibration when a member decides on reviewed work ([70cd8db](https://github.com/bkuberek/ropeshift/commit/70cd8dbf457c47e2d53ac05fc1fda4058ec4efd3))
* **control:** settle reviewed work from its verdicts ([ba58560](https://github.com/bkuberek/ropeshift/commit/ba58560c3281ea0e8e70204229009d60d06db357))
* **lanes:** tell the next attempt why its work came back ([4d61289](https://github.com/bkuberek/ropeshift/commit/4d6128983fe5e02f8b84abebdf98c6253b98af16))
* **mcp:** remove acceptance_record from the orchestrator ([1424a45](https://github.com/bkuberek/ropeshift/commit/1424a45add287f32ae25d226d0fc170370efa5b0))
* **measures:** measure review agreement per workspace and repository ([a2ab677](https://github.com/bkuberek/ropeshift/commit/a2ab677fc1accd50ef6aac0182683ecd27748076))
* **repository:** set the merge authority a member keeps ([483c0a9](https://github.com/bkuberek/ropeshift/commit/483c0a9f85896876a1a7cf3fd87b60d58e8b0dab))
* **store:** keep review verdicts bound to their commit and lane ([b4185e5](https://github.com/bkuberek/ropeshift/commit/b4185e557f144fdb973f1252c4f56096cf30c70a))
* **web:** show review agreement per repository on the operations page ([91a0a0a](https://github.com/bkuberek/ropeshift/commit/91a0a0a8e1a30290c54e2433153979b21f922116))
* **worker:** give a review lane the diff it reviews ([ebfc32f](https://github.com/bkuberek/ropeshift/commit/ebfc32fd69b79221d38be766c3939444e651f8cc))
* **workorders:** give a review a verdict per criterion ([7437e0f](https://github.com/bkuberek/ropeshift/commit/7437e0f8fe28c2d3aada29ad89c3f0ca93fbd741))


### Bug Fixes

* **control:** say what work waits for only while it waits ([a18bdeb](https://github.com/bkuberek/ropeshift/commit/a18bdeb49338f9b8c9760394c412de9e085299d2))
* **worker:** brief a cleared session only after it is adopted, and keep the brief owed until it lands ([4ab9c4b](https://github.com/bkuberek/ropeshift/commit/4ab9c4b03d7646ea9ec279d8f21c2d712e3f8c95))
* **worker:** leave a review's report to the control plane when it sent no criterion IDs ([f0b6d29](https://github.com/bkuberek/ropeshift/commit/f0b6d294396b0253edd5483105827c3e6539765b))
* **worker:** park a headless lane at once when its gated call's peek was missed ([3b3ac2c](https://github.com/bkuberek/ropeshift/commit/3b3ac2cb95ae034d7978ea2cf6d4787201d941da))

### Notes

* Every finished change is reviewed. When a `change` or `rework` lane completes with a commit, ropeshift creates a `review` work order. Its lane runs read-only in a detached worktree at the reviewed commit, with the diff, the contract, its requirements, and the documents it cites, and it can never be a lane of the work it reviews. It cannot edit, commit, push, or create branches, and its worktree is discarded when it ends.
* The review gives each criterion a verdict, `pass`, `fail`, or `cannot_judge`, with reasons and citations, bound to the reviewed commit. A fail sends the work back on its own, with the failed criteria and their reasons at the top of the next attempt's brief, for up to `max_review_rounds` (profile `[review]`, default 3) reviews; then the work waits for you.
* Acceptance follows the repository's merge authority, set with `ropeshift repository set <id> --merge-authority person_merges|person_approves|review_approves` (default `person_merges`). Only at `review_approves` does an all-pass review accept the work by itself; otherwise the work waits in Needs you with each criterion's verdict. A decision you make while a review is open cancels it.
* Your decisions after a review become a trust measure. Each accept or rework after verdicts records whether you agreed with the review, and the Operations page and `ropeshift repository show` show the agreement per repository beside its merge authority. The measure never changes the merge authority; only you do.
* The orchestrator's `acceptance_record` tool is removed; orchestrators no longer accept or rework work.
* A lane whose session was cleared is briefed only after ropeshift adopts its new session, and the brief stays owed until it lands, so a restart no longer sends the brief to a session that is not listening yet.
* A headless lane whose gated call slips past the worker's first check now parks for your decision at once, instead of after its wait runs out.
* This release adds database migrations `0036`, `0037`, and `0038`. They run on the first start.

## [0.1.0-alpha.7](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.6...v0.1.0-alpha.7) (2026-11-10)


### Features

* **authority:** give repository record changes their own action ([3a84fa7](https://github.com/bkuberek/ropeshift/commit/3a84fa773bebc948105427413f4e578ee50ac020))
* **cli:** show and set a repository's record ([f9f04bc](https://github.com/bkuberek/ropeshift/commit/f9f04bc4c21b8f99c16560e3e41c5f00b275d1d2))
* **control:** start a lane from its base or its dependency's work ([5aa1484](https://github.com/bkuberek/ropeshift/commit/5aa14844cb7b0c23309895bb0fe6fdba325ab1a2))
* **entry:** place the daemon's lanes from its checkouts ([e5f080d](https://github.com/bkuberek/ropeshift/commit/e5f080de3307b2c80ba030635265eac497325ea7))
* **gitcli:** read a base branch's tip and test commit ancestry ([b2531fa](https://github.com/bkuberek/ropeshift/commit/b2531fa0f8941f8b4f6ea757a4c665ee74a9b9a1))
* **intake:** detect a repository's remote and default branch when a source is read ([d2a5a7b](https://github.com/bkuberek/ropeshift/commit/d2a5a7bb215e189ede855ccb7531e0f9ca694c97))
* **planning:** read a work order's base ([110d3c6](https://github.com/bkuberek/ropeshift/commit/110d3c609d1061a1d328e4450971f21955edcc74))
* **worker:** start a lane's worktree at the base its claim names ([12fe4e9](https://github.com/bkuberek/ropeshift/commit/12fe4e9a552639e3e0f1499d18a2a1bc7623845e))


### Bug Fixes

* **approvals:** void a request when the call it holds moves ([df47db3](https://github.com/bkuberek/ropeshift/commit/df47db3c14a361dcb4bd95c3aa7e3756b8ac17d8))
* **cli:** name the plan and version a plan approval covers in approval show ([52cfd31](https://github.com/bkuberek/ropeshift/commit/52cfd31ae95e3758bbe095bc545d25d16899efce))
* **cli:** print the web address the daemon bound in plan start ([3210524](https://github.com/bkuberek/ropeshift/commit/32105241fd2deb72c6e72c1120788388ff5c270b))
* **control:** drop crash points the crash matrix has no row for ([8204ca0](https://github.com/bkuberek/ropeshift/commit/8204ca0130fe9b1e998063e119f54201d499588c))
* **top:** size the needs' kind column to the widest kind listed ([64e3dcb](https://github.com/bkuberek/ropeshift/commit/64e3dcb075765117f7816553c88d31ea2c2969d3))
* **web:** list the repository's other files by kind, without the ledger's records ([f085b06](https://github.com/bkuberek/ropeshift/commit/f085b06a9845a4aab6cef465fc0299af2f2549dc))
* **web:** name the milestone of a plan's work in words ([172e86d](https://github.com/bkuberek/ropeshift/commit/172e86d0076d1dce4d96fb3176bec52bcc4d2fb2))
* **web:** render Markdown tables and fenced blocks on a document page ([944fb2b](https://github.com/bkuberek/ropeshift/commit/944fb2bbe51a75eb7c0fd0abecda37ace8930ee2))
* **web:** shade the edge a document's code block runs past ([2c7de0e](https://github.com/bkuberek/ropeshift/commit/2c7de0e54bb1d1f8ddb70da7dacdec72aeac5c40))
* **web:** show a contract's milestone, shared use, sizes, and served requirements on its plan ([f6e1452](https://github.com/bkuberek/ropeshift/commit/f6e1452a07ce5f273d377e2d5be9c307f8df7c23))
* **web:** show the critical path's work orders and size points, and draw a bar's done share ([3d33527](https://github.com/bkuberek/ropeshift/commit/3d33527e1a7cb23d5365ea56f30c35ced8f153cf))
* **web:** size a lane's pane to its screen and scroll it inside itself ([2b3bf3c](https://github.com/bkuberek/ropeshift/commit/2b3bf3c1edabc9172441774b746361761a739f6a))
* **web:** trace each requirement through its contracts once ([e732970](https://github.com/bkuberek/ropeshift/commit/e732970769fc946d5139ff333b4641b264d37069))
* **worker:** wake no orchestrator while its brief's turn is still settling ([e2c8fa3](https://github.com/bkuberek/ropeshift/commit/e2c8fa3eecf630a86350e4eab55de09f425f78fa))

### Notes

* Lanes no longer start from your checkout's `HEAD`. Each repository has a record (`ropeshift repository list|show|set`) with its remote, default branch, base branch, merge method, and `start_from`. A lane starts from its work order's `base`, else the repository's base branch, fetched from the remote when there is one; uncommitted or unpushed work in your checkout no longer reaches a lane. A lane records the base ref and commit it started from, shown in `lane show`.
* Dependent work starts from its dependency's work. By default it waits until the dependency's accepted commit is merged into the base, shown as `waiting_for_merge` in `work show`; with `ropeshift repository set <id> --start-from accepted` it starts from the accepted commit instead, which the record refuses for the `squash` and `rebase` merge methods. A failed fetch or a missing base branch holds the work as `base_unavailable` and never falls back to `HEAD`.
* An approval request whose call changes before anyone decides it, such as a file swapped for a link, is now voided with the reason, cannot be approved afterwards, and is no longer recorded as answered at the terminal.
* An orchestrator is no longer woken twice with the same question while its first turn is settling.
* The web pages from the previous release are polished: the roadmap's critical path counts work orders and size points, progress bars show the done share, requirement tracing lists each contract once, document pages render tables and code blocks, plan detail shows milestones, sizes, and served requirements, and the lane pane fits a phone screen. `plan start` prints the web address the daemon actually bound, and `top` aligns its columns.
* This release adds database migration `0035`. It runs on the first start.

## [0.1.0-alpha.6](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.5...v0.1.0-alpha.6) (2026-11-10)


### Features

* **api:** stream a lane's pane and take its keys ([785af55](https://github.com/bkuberek/ropeshift/commit/785af554d07ae66aa87141403a73686827e171e6))
* **cli:** name the planning session's web page in plan start ([04d9517](https://github.com/bkuberek/ropeshift/commit/04d951781b066e9cfa46818a0cc8a91a52983bf1))
* **entry:** give the web pane the worker's terminal ([1acbcac](https://github.com/bkuberek/ropeshift/commit/1acbcac397a75e2b9d28c9957333717cad7f84e4))
* **tmux:** read a pane's screen for the web pane ([b47c3fc](https://github.com/bkuberek/ropeshift/commit/b47c3fc1622e608b54fe97f1970f531b7982f22a))
* **web:** approve a plan and review a contract from Needs you ([56d5f06](https://github.com/bkuberek/ropeshift/commit/56d5f0636c70c6898e3bfb74055259ef4ae7a1c8))
* **web:** show and type into an interactive lane's pane ([7516a0d](https://github.com/bkuberek/ropeshift/commit/7516a0d73a80b6b74e7a5011af2253e85eaf73ea))
* **web:** show document versions, review state, and requirement traceability ([4bff460](https://github.com/bkuberek/ropeshift/commit/4bff460bedcb18dc15a8e7f3a8cc5152d1144a17))
* **web:** show milestones, parallel work, and the critical path on the roadmap ([d70bac8](https://github.com/bkuberek/ropeshift/commit/d70bac878cc50c7e4a1c4e991f6d0e3bef6357de))

### Notes

* The planning session is in the web UI too. Open a lane's page to watch its pane and type into it, with a field for lines and buttons for Enter, Escape, Tab, Backspace, the arrows, Ctrl-C, and Ctrl-D. It follows the lane across a park or a restart, and it shows the same conversation as `ropeshift lane attach` in a terminal. `ropeshift plan start` prints the page's address. A web viewer does not yet count as attached, so with only the page open the planning agent's questions go to Needs you.
* Needs you can approve a plan, showing what changed since the last approved version and the draft decisions, and can pass or send back a contract review.
* The roadmap shows each plan's milestones with progress, the work that can run at the same time, and the critical path. Documents show their versions and whether a plan approval covers them, and each requirement traces to the criteria and work that serve it.

## [0.1.0-alpha.5](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.4...v0.1.0-alpha.5) (2026-11-10)


### Features

* **api:** show milestones, layers, and the critical path on the roadmap ([5c5f409](https://github.com/bkuberek/ropeshift/commit/5c5f409aa8c24349e652874567e25958d24486a3))
* **api:** show plan approvals and unreviewed contracts in Needs you ([f5b1f56](https://github.com/bkuberek/ropeshift/commit/f5b1f568bad73409793f28d85b8ad7095d653ccc))
* **authority:** add plan approvals that only a member decides ([5a37f09](https://github.com/bkuberek/ropeshift/commit/5a37f098bb23629a4049b4e19ed16c9e4a70690b))
* **cli:** approve a plan ([6c53e36](https://github.com/bkuberek/ropeshift/commit/6c53e366f60afc7d87bc0bd05c345afa2ff39123))
* **control:** approve a plan in one step, deciding its drafts and passing the review of the work it covers ([29c6ded](https://github.com/bkuberek/ropeshift/commit/29c6ded000ee440b94fe105ad796cb23c87115d8))
* **control:** ask a member to approve a planning lane's branch before anyone accepts it ([2d120fb](https://github.com/bkuberek/ropeshift/commit/2d120fb9618343fcd8a46dea8b46c9f4e59e7a1a))
* **control:** make a plan version when the plan digest changes and ask a member to approve it ([04095e5](https://github.com/bkuberek/ropeshift/commit/04095e521468efd0e000758a712811a17e6eea3a))
* **intake:** expand milestone and shared-contract dependencies ([b2e575a](https://github.com/bkuberek/ropeshift/commit/b2e575aa3530ce890507661fb5d962b740c3f3e5))
* **intake:** refuse a plan version with a dependency cycle ([1eb1730](https://github.com/bkuberek/ropeshift/commit/1eb1730cbd037b1d96541f4a869e0b676c924992))
* **intake:** require a milestone when the plan has milestones ([240c633](https://github.com/bkuberek/ropeshift/commit/240c6338193247a69159237c1a7353ce30609115))
* **intake:** resolve milestones and shared contracts within a plan ([3c925a6](https://github.com/bkuberek/ropeshift/commit/3c925a6567a6df8899541bb7588d70cd5c6c3414))
* **lanes:** tell a planning lane how its plan is approved ([ee99fce](https://github.com/bkuberek/ropeshift/commit/ee99fce53317516358f45f88cf1a06e44451aebe))
* **planning:** compute dependency layers, cycles, and the critical path ([8664535](https://github.com/bkuberek/ropeshift/commit/86645357f32a1766ff7a3bde98709311338454ca))
* **planning:** digest a plan over every record that names it ([208cc67](https://github.com/bkuberek/ropeshift/commit/208cc672575546c9bdc36d2a5df212f5c59642f4))
* **top:** name a plan that waits for approval and a contract that waits for review ([cd52ffc](https://github.com/bkuberek/ropeshift/commit/cd52ffcb4b4978d3bb40401c5b2687850f6e50fa))


### Bug Fixes

* **intake:** hold ready work back when it gains a dependency ([b395515](https://github.com/bkuberek/ropeshift/commit/b395515af1f1683c3b0304ed67501e1addf58b80))
* **intake:** name the milestones of no plan in a cycle problem ([d1c7f3d](https://github.com/bkuberek/ropeshift/commit/d1c7f3d9544a1c56ff38769673d54f27f1d93762))
* **planning:** leave canceled work off the critical path weight ([b0305b1](https://github.com/bkuberek/ropeshift/commit/b0305b13714b3ae4a2c4db65a5dd2a51c839657e))

### Notes

* Approve a plan in one step. When the planning agent finishes, or when a plan's files change, ropeshift records a plan version and asks a member to approve it. `ropeshift plan show <plan>` prints the pending approval, what changed since the last approved version, and the draft decisions; `ropeshift plan approve <plan>` approves it. Approval records the draft decisions as your decisions, accepts the planning work, and passes contract review for the work the plan covers, so that work becomes ready with no step per work order. Only a member can approve, and the planning agent cannot approve its own plan.
* Milestones order the work. A contract names its milestone, and when a plan has milestones every contract must name one. Work waits for the milestones its milestone depends on, and for the shared contracts it `uses`. A plan whose dependencies form a cycle is refused as a whole, with the cycle named. The roadmap data now carries each plan's milestones, the work that can run in parallel, and the critical path; the web page for it comes later.
* After upgrading, each existing plan gets a new version and asks for one approval, because a plan version now covers every document and contract in the plan, not only the plan file.
* This release adds database migrations `0033` and `0034`. They run on the first start.

## [0.1.0-alpha.4](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.3...v0.1.0-alpha.4) (2026-11-10)


### Features

* **cli:** attach to an interactive lane ([7471d8e](https://github.com/bkuberek/ropeshift/commit/7471d8e4da5bd808b8a995757970efb86c050e0e))
* **cli:** start a planning session ([c3edb1d](https://github.com/bkuberek/ropeshift/commit/c3edb1d2f5920be94da31022bdcfb9cbbb576dc4))
* **hook:** leave the question tool to an attached terminal ([194619b](https://github.com/bkuberek/ropeshift/commit/194619b89a4ddb98bbad643c16f83f4b5d1d97c8))
* **intake:** record document and plan versions ([1c11afd](https://github.com/bkuberek/ropeshift/commit/1c11afdbdbe3353e70a455497179709a3c3726f0))
* **lanes:** add the planner role and run planning lanes interactively ([baaac59](https://github.com/bkuberek/ropeshift/commit/baaac59bd5497073c5328d69f961eb7f52bc726d))
* **lanes:** leave AskUserQuestion to an attached terminal in planning lanes ([b76ee21](https://github.com/bkuberek/ropeshift/commit/b76ee215a2a101724e920e837f4cba58b580ba87))
* **lanes:** show every file format in the planning brief ([d853455](https://github.com/bkuberek/ropeshift/commit/d8534554c2839a7517ffcfd79c441e507fb10f97))
* **packs:** pin cited documents and notify lanes of new versions ([a4ff29d](https://github.com/bkuberek/ropeshift/commit/a4ff29dd43f7a98f988cad5358c425a622c94fdb))
* **planning:** list a parsed milestone with its own document kind ([f136e7e](https://github.com/bkuberek/ropeshift/commit/f136e7ece3ea452d2a2aeef1dc3ea66939a7b63a))
* **planning:** parse spec, architecture, experience-design, and milestone files ([9eadd2a](https://github.com/bkuberek/ropeshift/commit/9eadd2aaed4443fea603e5bcf9758350dfa5adeb))
* **questions:** file a planner's converted question under product ([ca62f43](https://github.com/bkuberek/ropeshift/commit/ca62f438d2e9abd92d60ae91147e1369e1c9e990))
* **questions:** send planner questions straight to the person ([1908d04](https://github.com/bkuberek/ropeshift/commit/1908d04caa6c88302be68bc214bafd684179d283))
* **ready:** require criterion requirement mappings when a plan has requirements ([1e34e93](https://github.com/bkuberek/ropeshift/commit/1e34e931032d0381975d9e2820eb0b267c3f3c13))
* **tmux:** attach a person's terminal to a pane and count its clients ([2a9073f](https://github.com/bkuberek/ropeshift/commit/2a9073f585b924a7c77c41c97034ccc9a07e349f))


### Bug Fixes

* **worker:** snapshot held lanes before reading lane records ([d3a680d](https://github.com/bkuberek/ropeshift/commit/d3a680d92a57dcd4f3d83683f0fa2723bba7b538))

### Notes

* Start a planning session with `ropeshift plan start --goal "..."`. It creates a `plan` work order, passes its review as you, claims it on your worker, starts the planning agent in a terminal pane, and attaches your terminal. Detach with `C-b d`; the session keeps running, and `ropeshift lane attach <lane>` rejoins it, following it across a park or a restart.
* A planning lane always runs in a pane. On a machine without tmux, its work order is blocked with reason `no_interactive_mode` instead of running headless.
* A planning agent's questions are yours. While you are attached, it asks in the conversation. While you are not, its questions go straight to Needs you, and no orchestrator decides or assumes an answer for them.
* Repository files gain the `spec`, `architecture`, `experience-design`, and `milestone` kinds, versioned by their committed content. A contract can cite documents, and each criterion can list the requirement IDs it serves. When a plan has a requirements document, a criterion with no mapping, or with an unknown ID, keeps that contract's work from starting. Files written for earlier releases stay valid.
* This release adds a database migration (`0032`). It runs on the first start.

## [0.1.0-alpha.3](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.2...v0.1.0-alpha.3) (2026-11-09)


### Bug Fixes

* end a session with its token alone on sign-out ([923bbe3](https://github.com/bkuberek/ropeshift/commit/923bbe3a37b18dea1353321ccd5992499216eaed))
* push a lane's work only when the user's profile turns push_on_stop on ([07c1d81](https://github.com/bkuberek/ropeshift/commit/07c1d817edc7f749764b72b1ac809c41ffd9ad13))

### Notes

* A lane no longer pushes its branch or checkpoint to the remote when it stops. To keep the earlier behavior, set `push_on_stop = true` under `[worktree]` in your own `~/.ropeshift/profile.toml`; a repository cannot set it. With it off, a lane's work stays on this machine, `ropeshift lane archive` needs `--force`, and a parked lane resumes only here.
* `ropeshift sign-out` ends a session with its token alone and never asks for a passphrase.

## [0.1.0-alpha.2](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.1...v0.1.0-alpha.2) (2026-11-09)


### Bug Fixes

* count and format what the views state ([b550087](https://github.com/bkuberek/ropeshift/commit/b55008782807abfa6e2c2243e86b79b3872b704b))
* give a session its grounding when the hook runs out of time ([5bfd3f3](https://github.com/bkuberek/ropeshift/commit/5bfd3f3f6ea40fc11a7d31675f91e20b27853bb4))
* ground a session when its hook's deadline comes before the policy read ([afaf92f](https://github.com/bkuberek/ropeshift/commit/afaf92f14ca491d3bb982026cd829a5dc6e212cc))
* hand off a hook's event when its deadline comes before its verdict ([06cfb07](https://github.com/bkuberek/ropeshift/commit/06cfb078689dcea762f1ef0b110f5ba0c8d9cdcb))
* keep a policy hook's verdict when its deadline meets the hand-off ([2cdd363](https://github.com/bkuberek/ropeshift/commit/2cdd3633f7d683d714f809b8e5ff0dcd020bbc7f))
* keep IDs on one line and name each lane's work ([b737e3c](https://github.com/bkuberek/ropeshift/commit/b737e3c663cb42da9664c9511a370d16ae59351e))
* let a status line held past its deadline exit 0 ([3ae5303](https://github.com/bkuberek/ropeshift/commit/3ae530338a5283b96f22c80dcc42b78e21fc4e6d))
* list every command and answer help in both binaries ([f2b33af](https://github.com/bkuberek/ropeshift/commit/f2b33afdb116651c2261f96307ed2d560e1fdd7a))
* print a top frame as wide as its terminal ([ff4bc86](https://github.com/bkuberek/ropeshift/commit/ff4bc8605fa52c8e19a1c8ca7df3b96e4f33a3e5))
* refuse arguments to ropeshift mcp ([3573c84](https://github.com/bkuberek/ropeshift/commit/3573c84fb35e814271e1bdf0644a0334340fd5cf))
* say counts and money in the terminal as the web UI does ([8881f8a](https://github.com/bkuberek/ropeshift/commit/8881f8a879b2a8cc1fef5a284c1fe2a3f6fe078f))
* say what a parked lane waits for in plain words ([647881b](https://github.com/bkuberek/ropeshift/commit/647881b03cec032d99ce00d0c104791c7c94e7c3))
* show a recommendation that is no option as chosen ([c4b8142](https://github.com/bkuberek/ropeshift/commit/c4b81427b3667c36fba8c6d3094c22b72c31a034))

### Notes

* The hook fixes correct `v0.1.0-alpha.1` behavior on a heavily loaded machine: a tool call the policy allowed could be refused with "the policy check ran out of time", a call the policy denied could receive two answers at once, a session could start without its grounding, and an event could miss the spool. Upgrade from `v0.1.0-alpha.1`.

## 0.1.0-alpha.1 (2026-11-09)


### Features

* accept completed work on evidence a lane did not write by itself ([792262a](https://github.com/bkuberek/ropeshift/commit/792262a9eab17db698b5fe213a1c72dae15e2c93))
* add claims, leases, and fencing ([0095733](https://github.com/bkuberek/ropeshift/commit/0095733c83e4232ec05a9074fda3e35469a4117d))
* add ropeshift top, an operator's live view of the fleet ([accfea1](https://github.com/bkuberek/ropeshift/commit/accfea19811e0d2d834fa241468e3b62eb559533))
* add the Claude Code headless driver ([5cc4bc6](https://github.com/bkuberek/ropeshift/commit/5cc4bc6a63b5e127b935865a5723a4b3eb8350e5))
* add the ledger core on SQLite ([d609798](https://github.com/bkuberek/ropeshift/commit/d6097984f3a1626cc5cd5a6601b2ae3a4af30cd3))
* add the module, entry points, and repository rules ([35f3c07](https://github.com/bkuberek/ropeshift/commit/35f3c079fe736d4e21873d54272905f5be58e4c7))
* add the repository files source and the definition of ready ([f583b01](https://github.com/bkuberek/ropeshift/commit/f583b01c6fc013a5e940fca36644be274562cdd4))
* add the worker link and journal ([6bbe4a5](https://github.com/bkuberek/ropeshift/commit/6bbe4a57a8a7ebd120ac785149e3fbbf1e8243e1))
* add the worker socket, the hook, and the MCP server ([93fb6ae](https://github.com/bkuberek/ropeshift/commit/93fb6ae77052cc4e3d5032bc021f6932e826a443))
* add worktrees, the lane environment, and thin profiles ([608c69b](https://github.com/bkuberek/ropeshift/commit/608c69b1270618b4bbd66585d8c8f1585d25a544))
* advertise a manifest from the doctor, layer lane profiles, and preflight the inbound policy ([6999d34](https://github.com/bkuberek/ropeshift/commit/6999d345eea35140e11052da202a52024077874d))
* ask through the ladder, and hold a human behind the gate ([aa2cdf1](https://github.com/bkuberek/ropeshift/commit/aa2cdf1729f8b3f7048337774cb38c88ad4b547d))
* bind an approval to its digest, and refuse a self-approval in the store ([104e8cd](https://github.com/bkuberek/ropeshift/commit/104e8cd4cae91cfe47382c85ef75a912d858fcb6))
* bracket every effect and reconcile after a crash ([e0440d2](https://github.com/bkuberek/ropeshift/commit/e0440d2002d0f3cfbe2f6f7c4f8a8e9111dc9dd0))
* build the execution and operations views ([4c402bb](https://github.com/bkuberek/ropeshift/commit/4c402bb3c940744c3a2aede400b1c7a36a251e4d))
* build the planning views ([1e21462](https://github.com/bkuberek/ropeshift/commit/1e21462aeab577a9ee574e160d1982ee2db81bfe))
* build the web shell and Needs you ([06e0083](https://github.com/bkuberek/ropeshift/commit/06e00838a94b9c107323771065a2caaa090fd2d1))
* compute the product measures from the ledger, and keep an experience record per lane and per decision ([74db045](https://github.com/bkuberek/ropeshift/commit/74db04573ed9958d8738dc6fab50edb5d23f05a4))
* decide and assume within a versioned policy, and overturn in one transaction ([248fa12](https://github.com/bkuberek/ropeshift/commit/248fa127a0f196153503bb86c72612c8e8484020))
* deliver messages to lanes ([c8c3fd8](https://github.com/bkuberek/ropeshift/commit/c8c3fd8bbb4eafa6de673b92b8cdcfbd6024f18d))
* deliver questions to members in batches, and take one answer per question ([1c49cbb](https://github.com/bkuberek/ropeshift/commit/1c49cbb52465e259770bc5fa37121c7e4d27ef6a))
* deny tool calls from a policy compiled for the generation ([63b3507](https://github.com/bkuberek/ropeshift/commit/63b35075db80b1e354a93cb04a8656033d5ad684))
* derive lane state, liveness, and reachability ([2a95e4c](https://github.com/bkuberek/ropeshift/commit/2a95e4c9284863f3565c02b99da4784c30a0e879))
* give every operation a command, and hold that to the routes ([6f6b0dd](https://github.com/bkuberek/ropeshift/commit/6f6b0dd071432e9fd5ffa911571324447841dad3))
* guard every stop, hold per-lane resources, and run profile hooks ([ebde8df](https://github.com/bkuberek/ropeshift/commit/ebde8df8bc5a7ad5b40d511ce43cf3ae2e907f2d))
* hand a lane to its next generation under a contract that cannot block ([e10dcda](https://github.com/bkuberek/ropeshift/commit/e10dcda036d6a0eea1b96bb441229f263c3dc32e))
* hold a worker to its autonomy level and a member's run windows ([9b91e74](https://github.com/bkuberek/ropeshift/commit/9b91e74c72cb0a0f5df5bee5065c94779a5f828a))
* hold caps at claim and at start, and control the fleet with one mode ([5391d74](https://github.com/bkuberek/ropeshift/commit/5391d743a2116b58f0fd6a801c2d4192a962a4c6))
* hold each lane to its context thresholds during a turn ([e791c9a](https://github.com/bkuberek/ropeshift/commit/e791c9a09f0be5405a95cd4b9be09b014692053e))
* hold headless sessions in a lane shim ([bbd27be](https://github.com/bkuberek/ropeshift/commit/bbd27bed6875d874942f6c669848a745664d5102))
* let a member cancel work, and stop its lane through desired state ([c1b0937](https://github.com/bkuberek/ropeshift/commit/c1b09379d4888bd6291637fecb219088828ef86f))
* let an orchestrator decide what a member delegated ([fefcaf8](https://github.com/bkuberek/ropeshift/commit/fefcaf8236a3e15175bd1480f78ce757464980bb))
* park a lane with its process gone, and evaluate its wake in the commit ([6c89b07](https://github.com/bkuberek/ropeshift/commit/6c89b07f9e7220037fbfcc86094aa4e45a0be325))
* park on usage limits and expired sign-ins ([69caf6f](https://github.com/bkuberek/ropeshift/commit/69caf6fbed011c7b43dc24c4a6d7323b52a35113))
* pin a context pack at each claim ([02db5b7](https://github.com/bkuberek/ropeshift/commit/02db5b7478a5b1b225d046100b1ca1acd902c776))
* pull work and work the ladder through the orchestrator's tools ([d64eb33](https://github.com/bkuberek/ropeshift/commit/d64eb337e070dc5dbc192caec3dfcf23d5c05d45))
* qualify Claude Code 2.1.317 ([1810497](https://github.com/bkuberek/ropeshift/commit/1810497348d3b277ab0eb2bdcb603c694086725a))
* qualify Claude Code 2.1.319 ([4c3d33f](https://github.com/bkuberek/ropeshift/commit/4c3d33f8a4d6f9fa35d4698b0b847c82c5b8180c))
* qualify each Claude Code surface by its own check, and probe a new version before work runs ([132bc59](https://github.com/bkuberek/ropeshift/commit/132bc5939e99ac284d95ee19f729cc39d94fb1e7))
* read every file kind, add knowledge documents, and carry work through planning ([4abc8dc](https://github.com/bkuberek/ropeshift/commit/4abc8dc7a822722c4779f4ebd1f2d2a33dac7cbf))
* record a clear as a generation, and re-ground a lane in its identity ([0fc645e](https://github.com/bkuberek/ropeshift/commit/0fc645ebe73fd4fac71c43f3690ed34e81b135e9))
* record questions and decisions, and rank candidates through search ([3402e7d](https://github.com/bkuberek/ropeshift/commit/3402e7da8e88b2e36bedf8fa847625aa2d44ece2))
* report the release version, commit, build date, and changes in every binary ([c5a6764](https://github.com/bkuberek/ropeshift/commit/c5a6764f9d34f6308deab76b9f731799ea29bc5b))
* resume a woken lane by the first row of the resume table ([6199a7f](https://github.com/bkuberek/ropeshift/commit/6199a7fe6757f2d0be22c9685788ffde7a359e4f))
* run lanes in a pane behind a checked start ([7da58a4](https://github.com/bkuberek/ropeshift/commit/7da58a42100b8f979962f3002b93e483ccbed1fd))
* run one orchestrator per worker, woken in batches ([41ca1e6](https://github.com/bkuberek/ropeshift/commit/41ca1e6b49012634ebc6c086ec8765df3c89e571))
* run planning and contract review as lanes ([af2dcc4](https://github.com/bkuberek/ropeshift/commit/af2dcc4c364aac4129ed26d4995e349bb3667214))
* run the daemon as a launchd agent or a systemd user unit ([9beee80](https://github.com/bkuberek/ropeshift/commit/9beee8007386899734e08ba51e8c727c42ce02ed))
* run the first lane end to end ([a7cf3b2](https://github.com/bkuberek/ropeshift/commit/a7cf3b2f8eaee3f6ee364c964c74e42fc6e74a42))
* say which of requirements, specs, and designs a document is ([5e759c3](https://github.com/bkuberek/ropeshift/commit/5e759c3c9c7016ffa4525c62ae5e946bd49a4ee4))
* send an answer to every lane that waits on it ([52cfcee](https://github.com/bkuberek/ropeshift/commit/52cfcee3c3fd6c11a1d74c8dd42761f5fd9ef3cf))
* serve a read model for every view, an event stream, and a web listener ([c75654f](https://github.com/bkuberek/ropeshift/commit/c75654f380f2c256deffe174ee36ed96d0834860))
* serve the control plane API and the work CLI ([3758ba6](https://github.com/bkuberek/ropeshift/commit/3758ba64a5eaf1b7c01ee0084d491ee0efcd4cd9))
* set up a machine with ropeshift init, and back up the ledger online ([28fc8d2](https://github.com/bkuberek/ropeshift/commit/28fc8d282e43ec9d2d3e18e27ca73212c9385ece))
* show a stuck orchestrator, and give a person something to do about it ([36bf3bc](https://github.com/bkuberek/ropeshift/commit/36bf3bce706ff53a290ae55594a12cdb05ddefa8))
* sign members in with a local passphrase, and route every check through one function ([8059b7e](https://github.com/bkuberek/ropeshift/commit/8059b7e6b5cf894de56830c456e46828dbea3df4))
* solo mode, CI, and automated releases ([fb857c4](https://github.com/bkuberek/ropeshift/commit/fb857c43a93a9735acbfb3986061c55bb3b7ef51))
* store wake conditions as data and fire each one once in the ledger ([041b841](https://github.com/bkuberek/ropeshift/commit/041b8413d6c4df25a0a80b9f55317b97602a70f5))
* supervise lanes with a breaker, restarts, and human resets ([e9939c7](https://github.com/bkuberek/ropeshift/commit/e9939c7aa1728ff80130f4e8538702a59498032a))
* tag each lane's telemetry, keep it on the machine, and export only when asked ([97850b4](https://github.com/bkuberek/ropeshift/commit/97850b490001645c0803f58f342b1de491853921))
* tell members what waits, through the web UI, the desktop, and a webhook ([20ff9d5](https://github.com/bkuberek/ropeshift/commit/20ff9d55e03b0c409d6283f4a0f90097b560353e))
* tell pinned lanes when a decision or a contract moves ([efdc437](https://github.com/bkuberek/ropeshift/commit/efdc437fec0b860292af62b66edff47d6042598a))


### Bug Fixes

* bind a decision to where a call lands ([a3855ad](https://github.com/bkuberek/ropeshift/commit/a3855ad5ebc07de56a4906f5c628c7fe790ac13a))
* bound a hung compaction by the stall limit ([2dee37e](https://github.com/bkuberek/ropeshift/commit/2dee37e6c45fd9bc21618a722da4405ed3aca025))
* bound the two holds a stalled compaction kept ([14d5ccb](https://github.com/bkuberek/ropeshift/commit/14d5ccbb628adda0f7c7da91519ba7b29013e811))
* check the login on a sign-in nobody could read, and fall back to tmux with no headless lane ([2b23df4](https://github.com/bkuberek/ropeshift/commit/2b23df44d857f1007026045e66e95872236506ef))
* count a restart delay from the crash, not from the charge ([dcefeb1](https://github.com/bkuberek/ropeshift/commit/dcefeb15edf3c08be72ca08d5feac889b22747f3))
* decide a limit's steps in the domain, and hold a probe's word to its proof ([cada081](https://github.com/bkuberek/ropeshift/commit/cada081eea403aa7549d46e6c305bed37f1d37dc))
* deliver a shim's shutdown reply before it ends ([5dbc949](https://github.com/bkuberek/ropeshift/commit/5dbc9497b99314902ff51d8f8f896f9da2b31344))
* draw no nudge from the turn a clear ended ([48db7cc](https://github.com/bkuberek/ropeshift/commit/48db7ccfc099195d2dafdb9f7625cda39261d694))
* end a probe's screen check when its pane dies ([1545437](https://github.com/bkuberek/ropeshift/commit/15454379d74f3bf27ee10ba29924b30728c11786))
* end a shim's lock with the shim that took it ([c8d9caa](https://github.com/bkuberek/ropeshift/commit/c8d9caaf413b2a69889922b84b1e367fb83e0476))
* end a tool call that never reaches PostToolUse ([154db96](https://github.com/bkuberek/ropeshift/commit/154db9694d05324ae3c0da0ccdc29337a82d7ae2))
* end the one-daemon lock with the daemon that took it ([8b8b649](https://github.com/bkuberek/ropeshift/commit/8b8b64951f81d92f90d682984b6ba456c6f06011))
* end the shim of a refused start before the refusal returns ([047c596](https://github.com/bkuberek/ropeshift/commit/047c596f190844bf1cf1242da53d26069c6cf833))
* export what the emitter last flushed as a daemon stops, within one timeout ([6eda39a](https://github.com/bkuberek/ropeshift/commit/6eda39a0624dc392b9e5c558ea80c7072b708752))
* give a hook that re-grounds a session time to hear its generation ([c85c6f4](https://github.com/bkuberek/ropeshift/commit/c85c6f40b32cc522190ca9fbf110d1a386d6ea4d))
* give a lane's pane a real-session suite's pane flags ([84126ee](https://github.com/bkuberek/ropeshift/commit/84126ee02b4c5f57716de2616079d62558414f96))
* guard the lane's session handle against a heartbeat ([db16eb4](https://github.com/bkuberek/ropeshift/commit/db16eb4489bf6f426cd4009a96482eb4f24c0309))
* hold a handoff, a resume, and a respawn to the day's cap on generations ([968e982](https://github.com/bkuberek/ropeshift/commit/968e98202987ee2cfd2390ea472b83df7f7e02b6))
* hold a look at a call to the lane of the work order it names ([b95dad3](https://github.com/bkuberek/ropeshift/commit/b95dad30f2d6f7d91865081f185d117711caf38c))
* hold a progress save to its generation ([8a12b81](https://github.com/bkuberek/ropeshift/commit/8a12b815393dccb9add00f446b2e8c61ee3f8049))
* hold an orchestrator at manual to the ladder for a delegated request too ([84905b8](https://github.com/bkuberek/ropeshift/commit/84905b804df669295e8c27cfcf8ad803058168b5))
* hold an orchestrator to its own tools ([024b25a](https://github.com/bkuberek/ropeshift/commit/024b25a006b19b9591415bb046ce864118461f82))
* install the newest pre-release when no release is stable ([ca45a1e](https://github.com/bkuberek/ropeshift/commit/ca45a1e297555f03d65dc82dc3aedf2cca0b3b61))
* keep a compaction's verdict where it lands ([f56caa5](https://github.com/bkuberek/ropeshift/commit/f56caa5dc3652bcda74670e6f28165f9481eedf0))
* keep a deny rule from naming the worktree through a link ([298984d](https://github.com/bkuberek/ropeshift/commit/298984d3e68522c133f4f870c77807c3dffdb469))
* keep a generation's session when a clear outruns its binding ([980b9e7](https://github.com/bkuberek/ropeshift/commit/980b9e7bcbec1e56993738d2f698682b1e378544))
* keep a lane's place across a compaction ([3a2a922](https://github.com/bkuberek/ropeshift/commit/3a2a92243bba80c2cbbc38467480196be41678ac))
* keep a native resume in the run mode its session started in ([2e86846](https://github.com/bkuberek/ropeshift/commit/2e8684639aa7531430da531f8a413249cdfa38f7))
* keep a problem an accepted plan's commit found until a later commit fixes the file ([f43d691](https://github.com/bkuberek/ropeshift/commit/f43d6914440a1ea8610d942b7bfbb0556351eb17))
* keep a sign-in's memory behind its journal ([0c16583](https://github.com/bkuberek/ropeshift/commit/0c16583e287b125d0bf0612fb76f2467725dcde3))
* keep an answer past its window for the next generation ([c073ae4](https://github.com/bkuberek/ropeshift/commit/c073ae41fe30601906f6a4daa695ca539f069853))
* keep the delegation check in the authority package ([a893dec](https://github.com/bkuberek/ropeshift/commit/a893decb7b393b02b9815c0dc2ac3716cb6ac789))
* keep the literal status read, and a parking lane's reach ([5ecfc2c](https://github.com/bkuberek/ropeshift/commit/5ecfc2c00886062f2a898469ed8e4364983adf3d))
* keep the long prompt of the echo test under Linux's limit on one variable ([ed9f705](https://github.com/bkuberek/ropeshift/commit/ed9f7054d21b0804281509eb76b1bbb0f2cb0a93))
* keep the plan a review records out of what a person reads ([33bd64f](https://github.com/bkuberek/ropeshift/commit/33bd64f508eb9e884eb25f3479aaafbdbb23b2b8))
* keep the session of a generation a clear ends ahead of its binding ([bd942be](https://github.com/bkuberek/ropeshift/commit/bd942be3b47410245a28b1b02568e10a897bba16))
* keep what a first prompt states before its session starts ([bffd741](https://github.com/bkuberek/ropeshift/commit/bffd741600e337fa0608b226fe54f088a548b680))
* kill a clear's brief before its flag moves ([b912296](https://github.com/bkuberek/ropeshift/commit/b91229644a3b7e4d8b042106762b8136cb6bfd9a))
* label an orchestrator's state, and keep a document of any kind in a list ([017993c](https://github.com/bkuberek/ropeshift/commit/017993c3f15ca0411cc7bfde1341e19232b7676d))
* leave an effect to the goroutine already performing it ([b492e52](https://github.com/bkuberek/ropeshift/commit/b492e523da3899490f877df983cbc3b85430d853))
* let a lane stage and commit in its own worktree with no prompt ([c5d04ba](https://github.com/bkuberek/ropeshift/commit/c5d04baa2bf339540247040c519b930f4811e4ee))
* let a limit warning with a low figure start new lanes ([4f25242](https://github.com/bkuberek/ropeshift/commit/4f25242efc277c3d5508a2495a4bf1e709f599c5))
* let a member take work out of blocked ([f8c3f21](https://github.com/bkuberek/ropeshift/commit/f8c3f21a1cafaffda108d5b9838cf6dee1e729ac))
* let a near-limit reading with no reset lapse ([7812f6e](https://github.com/bkuberek/ropeshift/commit/7812f6e9bcdbe03b6fa19cb3f1d05a6d2bf4b62a))
* let a page open a document and read its versions ([4ee30dd](https://github.com/bkuberek/ropeshift/commit/4ee30dd225b1386060354f7ceaceebeec991dded))
* let a stop come before the export queue ([671359b](https://github.com/bkuberek/ropeshift/commit/671359b06659cf482d2cacc279277ad7bfcc7f40))
* let a stopped orchestrator's run let go of what its last turn held ([c2a665b](https://github.com/bkuberek/ropeshift/commit/c2a665ba1a7f4dedde07bc609703a284dc0c746c))
* let a wake repeat after a kill rather than lose it ([e2d7da3](https://github.com/bkuberek/ropeshift/commit/e2d7da314a8e317e86d55262a4f4b8273e42c529))
* let go of a question when an orchestrator's turn ends with no record ([3ec4dd2](https://github.com/bkuberek/ropeshift/commit/3ec4dd2a4669703acb3e6e0d1d75678525d42b17))
* let the driver own a probe's directories, and wait for a probe as for a lane ([7987256](https://github.com/bkuberek/ropeshift/commit/798725662c9f640f8a9124809723f92d36b52b42))
* list a lane's artifacts in an order every read agrees on ([f610522](https://github.com/bkuberek/ropeshift/commit/f610522f3ecc04931c9b0838d5de0249988fe0b4))
* list a view's documents outside the read of the ledger ([9420fe7](https://github.com/bkuberek/ropeshift/commit/9420fe7d462c921013f5049ecf7a0bc2fc11764f))
* list every way work ends, and work no plan holds ([179e6dd](https://github.com/bkuberek/ropeshift/commit/179e6ddde31d3011cb40578bdee95a755e437b4c))
* match a category filter as a name, never as a pattern ([2fe24a4](https://github.com/bkuberek/ropeshift/commit/2fe24a41ec4c9546076a206070478b9af8c5331a))
* move a lane's state in the write that resumes it ([a515a22](https://github.com/bkuberek/ropeshift/commit/a515a220587b2d83adefa2764c2379d92def6b29))
* name the lane's variables in the generated MCP configuration ([b9749de](https://github.com/bkuberek/ropeshift/commit/b9749de419545c459e1b4ab5ba4da113a16cae18))
* open a stream with a first line, so a quiet ledger reads as live in WebKit ([a05515d](https://github.com/bkuberek/ropeshift/commit/a05515d28341023300300657dbaee26c24d29c80))
* raise nothing for a hook behind an answered prompt ([ebb6091](https://github.com/bkuberek/ropeshift/commit/ebb6091f169ba4c2d20298b3b700a8846da28613))
* read a call's fields by their exact keys, and hold a class to one field ([f7cbed5](https://github.com/bkuberek/ropeshift/commit/f7cbed54755337e8202903af284d591265dc27c0))
* read a commit that never held the ledger's version as a conflict ([62bb423](https://github.com/bkuberek/ropeshift/commit/62bb4231f3ab673d68f0d4ee0ba0c50ca166fa76))
* read a dead pane's exit on a tmux that has not reaped its process ([86fdd3f](https://github.com/bkuberek/ropeshift/commit/86fdd3fa4bca26ffdc73f297c856cfd1c63a2dd7))
* read a pane's signal when tmux prints it as a number ([1c8a466](https://github.com/bkuberek/ropeshift/commit/1c8a46696e33b6b6c78cea7150c665418730bb7b))
* read a person's claim through the one authorization function ([1f21f51](https://github.com/bkuberek/ropeshift/commit/1f21f5161d7d54e4bbb16eb088037aed2c63def5))
* read a whole scope before a wake calls a decision missing ([4884042](https://github.com/bkuberek/ropeshift/commit/488404210684fb36a35ae96f1c0412359eb285f8))
* read what lanes hold before the lanes, so a pass cannot free a starting lane's ports ([b2109ec](https://github.com/bkuberek/ropeshift/commit/b2109ec5db3428de8b3a42afd925bb20189a5aaa))
* refuse a renewal after the lease deadline ([9c2a2ec](https://github.com/bkuberek/ropeshift/commit/9c2a2ec0f70e9bb46a44a2bbb19d37ee320d803d))
* refuse an agent CLI that fails its version check ([bc5bfd3](https://github.com/bkuberek/ropeshift/commit/bc5bfd38f3818a0c4682f34b4080b7f4b950f5e5))
* seal a spool line with its lane's token ([0eadec0](https://github.com/bkuberek/ropeshift/commit/0eadec0b9889f6f701f215f8e5804516358b90cf))
* send a lane's status once per generation ([15c2654](https://github.com/bkuberek/ropeshift/commit/15c2654fd6ea960d6edbc5ce6d5c7a432772ce3d))
* spool a hook's event when the hook's deadline meets it ([15f0cdb](https://github.com/bkuberek/ropeshift/commit/15f0cdbcf8c5f810f990380103abb7f6c5ac2539))
* stamp a lane's status with the generation it was read under ([c67c320](https://github.com/bkuberek/ropeshift/commit/c67c320a518e14b8bd1a0848c9266eb31cef29a4))
* start no session on a lane that lost its lease while it was set up ([bde689a](https://github.com/bkuberek/ropeshift/commit/bde689a0f19906a13f170febc32f2c39cc79bcd9))
* state what a candidate says in an orchestrator's next question ([3064352](https://github.com/bkuberek/ropeshift/commit/3064352abeeea6c7579a51d9f218301d02511890))
* stop a lane no desired state names, whatever the beat can say ([1e47f3a](https://github.com/bkuberek/ropeshift/commit/1e47f3a066e1e6aad4105ebb7f1b7d5728f7ee95))
* stop a lane whose lease went before its session was reached ([2d5394b](https://github.com/bkuberek/ropeshift/commit/2d5394b9248ceebe0a177192379808fb6f16bec5))
* tell a document that is not there from a read that failed ([80986ef](https://github.com/bkuberek/ropeshift/commit/80986ef9f77077b69f93bd69097e51bb8a935405))
* tell a resumed session nothing of a call it deferred ([f1b3764](https://github.com/bkuberek/ropeshift/commit/f1b3764533ba0ed8705641a4f98abfaedf67cd99))
* tell a session that comes back the exact call a decision covers ([65e31c3](https://github.com/bkuberek/ropeshift/commit/65e31c397d07daf05c19d4d64c611ea289dc5ca3))
* tell an orchestrator that every question it takes ends in a record ([d5385ee](https://github.com/bkuberek/ropeshift/commit/d5385eea76b8f191da362c10d42cb26e32bc20a2))
* turn echo off before a passphrase prompt, and back on however it ends ([2e62271](https://github.com/bkuberek/ropeshift/commit/2e6227151b4ba59749e9cf9cda1f7697fdf9b5dc))
* type the guard statement for both dialects ([efa57e3](https://github.com/bkuberek/ropeshift/commit/efa57e3ff82d28a609ed95286cfde6096e655dd7))
* write the spans a daemon has not yet read from the ledger as it stops ([5b9989c](https://github.com/bkuberek/ropeshift/commit/5b9989c88f14b4c1b7dd541fd36adf66f55b9da0))


### Reverts

* put a clear's crash step back after its flag ([1d66255](https://github.com/bkuberek/ropeshift/commit/1d662553b305a99b9410e90d35457021a9f6c896))
