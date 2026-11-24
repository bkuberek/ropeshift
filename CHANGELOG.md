# Changelog

## [0.1.0-alpha.17](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.16...v0.1.0-alpha.17) (2026-11-24)


### Features

* act on decided paths and check a lane's commits before a push ([4c592ec](https://github.com/bkuberek/ropeshift/commit/4c592ecae756432c8ba087a08ed793289855ece3))
* act on decided paths and check a lane's commits before a push ([2f43195](https://github.com/bkuberek/ropeshift/commit/2f43195b4b6c0f0a9caadad620fbf839fd171a74))
* **api:** give each Needs you item a tier, and each question its asker ([2507a7b](https://github.com/bkuberek/ropeshift/commit/2507a7b920c6b728000917017700da7f393ab471))
* **api:** read the scrollback of a lane's pane ([98920f1](https://github.com/bkuberek/ropeshift/commit/98920f1235b207f02297c86683426e0d593881d9))
* ask each question with a title, tradeoffs, and what it unblocks ([1bc84d5](https://github.com/bkuberek/ropeshift/commit/1bc84d5233715e2a7bf54324b58f00a5481f5dca))
* ask each question with a title, tradeoffs, and what it unblocks ([a9ca29d](https://github.com/bkuberek/ropeshift/commit/a9ca29d57e11dcb528a0bc6a861ba42316fd223e))
* **autonomy:** ask a member to accept a one-way acknowledgement on every worker ([265f923](https://github.com/bkuberek/ropeshift/commit/265f923b4dbeba505d86b70b2ce745bbc6b610d4))
* **autonomy:** let a member withdraw a worker's one-way acknowledgement ([e971ebe](https://github.com/bkuberek/ropeshift/commit/e971ebec39b47e39c54d4f84ae840d1fd6cf0396))
* **autonomy:** let a member withdraw a worker's one-way acknowledgement ([54b7eb9](https://github.com/bkuberek/ropeshift/commit/54b7eb9ce61be6bac9a8c4accd87499c30f7a3fe))
* cancel a consultation whose question is decided while it runs ([fd1959d](https://github.com/bkuberek/ropeshift/commit/fd1959d6301e7ec3b4d36f03f70863d4f5b14ab0))
* **claudecode:** keep a probe's evidence of the checks that did not pass ([6c6b1fa](https://github.com/bkuberek/ropeshift/commit/6c6b1fad933e9e38e0cbc79c051f6b43b62c965b))
* **claudecode:** keep the worktree's MCP servers and leave out the member's own instructions ([75d2023](https://github.com/bkuberek/ropeshift/commit/75d2023b3fd0912d50ff742367ddc4d99a7a1827))
* **claudecode:** run lanes clean of the member's own CLI setup ([7b2b25a](https://github.com/bkuberek/ropeshift/commit/7b2b25ab7ac2bfbe11b2bf58b52ebf638507f1ee))
* **claudecode:** start every session clean of the member's own CLI setup ([ca60ad1](https://github.com/bkuberek/ropeshift/commit/ca60ad11744f511d54a44b8f7c401572acc7cd59))
* **cli:** read a lane's scrollback ([6daa334](https://github.com/bkuberek/ropeshift/commit/6daa334668e622d2ba80ba70fc724f9d65b9c1b7))
* **cli:** say in worker doctor when the mark of init's daemon is set ([4d40c6a](https://github.com/bkuberek/ropeshift/commit/4d40c6a4afafc8ad6081e7c58044689ab039da11))
* **control:** let an unattended orchestrator decide one-way choices the policy leaves it ([6105ccc](https://github.com/bkuberek/ropeshift/commit/6105cccebb8f3fba7e6be2d2caf0f06720385144))
* **control:** show the agent a lane runs as, and why a woken lane changed it ([e8fb61a](https://github.com/bkuberek/ropeshift/commit/e8fb61a7281dd2f491925c19bbbc423dac625859))
* **control:** start the orchestrator with its worker and show what lanes wait on ([1cecc13](https://github.com/bkuberek/ropeshift/commit/1cecc13feb2ff6d9bedece616ae88fd2e9a28a36))
* **decisions:** add a Keep operation for a member to keep a decision by its ID ([00358f3](https://github.com/bkuberek/ropeshift/commit/00358f37dde99db4f37675d7e7e4261f19adfc47))
* keep a lane's checkpoints and salvage to its own files ([5513992](https://github.com/bkuberek/ropeshift/commit/5513992cf1e96760a543f26c0d23d73127b12379))
* keep a lane's commits and checkpoints to its own files, and let the orchestrator decide the rest ([8751a4c](https://github.com/bkuberek/ropeshift/commit/8751a4c544e8e175b6f28fecb3b9e370fae11a79))
* **lanes:** block a session's reload of its own settings that widens what it runs, and hold the lane for a member ([df50eb5](https://github.com/bkuberek/ropeshift/commit/df50eb51757c4160491eb23cc56b43d8c9f5a8e5))
* **lanes:** hold a lane that adds or changes a hook in the frontmatter of its skills, subagents, or commands ([e909747](https://github.com/bkuberek/ropeshift/commit/e90974716e6a7170491cd9317beb9d2671e5b40b))
* **lanes:** hold a lane whose own settings widen what it runs, for a member's choice ([e220ffc](https://github.com/bkuberek/ropeshift/commit/e220ffcc7698ee864172c819ab1adf63c3a011d6))
* let the orchestrator decide each path a lane left out ([e1544c7](https://github.com/bkuberek/ropeshift/commit/e1544c7a2ee73daeb2e3add25d54a2dad61d243a))
* mark gate requests, and leave them out of Approve all ([f163c25](https://github.com/bkuberek/ropeshift/commit/f163c256c2b4dc656af6255cd333eadd981adc47))
* name the branch of each plan card that shares a version with another ([c909d98](https://github.com/bkuberek/ropeshift/commit/c909d98800a4f148150e4702aaddccdf92f96add))
* Needs you flow and recommendation-first question cards ([8a11bf3](https://github.com/bkuberek/ropeshift/commit/8a11bf3b4a0570250056e8a38f5e6a98c097a96f))
* **needs:** show an unattended orchestrator's one-way decisions for review ([7f0fa78](https://github.com/bkuberek/ropeshift/commit/7f0fa781f515c2ebafd4c28beeee028c6a7eca9a))
* **orchestrator:** consult a stronger read-only agent on a hard decision ([3425803](https://github.com/bkuberek/ropeshift/commit/34258034cc7dcede942a8c8a9091658d6744a2df))
* **orchestrator:** consult a stronger read-only agent on a hard decision ([18ceba9](https://github.com/bkuberek/ropeshift/commit/18ceba98f280835fb36735770e346d8d09cccf59))
* **orchestrator:** record a consultation's approval on its event and in its cost line ([d6c001e](https://github.com/bkuberek/ropeshift/commit/d6c001ea9b8a5a04009b05a92361e71119d65156))
* point a held plan request to the branch that finishes it ([b39c863](https://github.com/bkuberek/ropeshift/commit/b39c863b597b8c8d6fa4e4584bd2a8ac70cdbb8b))
* raise leaked personal setup once per machine, version, and piece ([2a540ed](https://github.com/bkuberek/ropeshift/commit/2a540ed72b9777123f9191d02a96fc05f8d66071))
* **roster:** add the consultant, the read-only agent a consultation runs ([ed255e0](https://github.com/bkuberek/ropeshift/commit/ed255e0979e8684303cb1d95cc7f6fbc118600fb))
* scroll back through a lane's pane, and copy all of it ([1addf59](https://github.com/bkuberek/ropeshift/commit/1addf598ea5e4012d9644835f973e456e1afac5a))
* show the recommendation's tradeoff while the other options are folded ([26b4c4f](https://github.com/bkuberek/ropeshift/commit/26b4c4fd5a3852fddfcebabc78c52513b5ae8c62))
* **spend:** add consultation_limit, the most one consultation may spend ([7b54b55](https://github.com/bkuberek/ropeshift/commit/7b54b55a94cb0467cacdfcefa03ebc7f1ba54868))
* **web:** agree with a question's recommendation and move through Needs you ([1492f54](https://github.com/bkuberek/ropeshift/commit/1492f545c75b104259624823622032b85f6ccbd0))
* **web:** copy, search, and fill the view with a lane's pane ([ef55c05](https://github.com/bkuberek/ropeshift/commit/ef55c05675333970493af50d7986f5d2dd13bdf3))
* **web:** list each gate a lane's Approve all leaves out above it ([af39ae2](https://github.com/bkuberek/ropeshift/commit/af39ae2d4a01f5b3590067104e3e8c71493b8819))
* **web:** show the scrollback tmux keeps above a lane's pane, and copy all of it ([a3edbaf](https://github.com/bkuberek/ropeshift/commit/a3edbaf589edc5d0a9373b2ef015a95465fa01d7))
* **web:** show what each lane runs with, and raise the member's own setup in Needs you ([d09f7a4](https://github.com/bkuberek/ropeshift/commit/d09f7a4c6cc6bc3ab4fe331dbec56d6617b5527f))
* **worker:** record on each lane what its session loaded at its start ([e367293](https://github.com/bkuberek/ropeshift/commit/e367293308a1e47e2df4ff6f9f4d24b1fbeedd0d))


### Bug Fixes

* accept the planning work a plan request names when approving a version the ledger holds ([7a9b14e](https://github.com/bkuberek/ropeshift/commit/7a9b14e553e4c307658144dacfef3069510f1ba5))
* act only on paths a worker reported, and never ignore a tracked or unsafe one ([21901d7](https://github.com/bkuberek/ropeshift/commit/21901d754cb9db0a371596be07793504bf06e353))
* answer a lane's first prompt before surveying its start ([d24b300](https://github.com/bkuberek/ropeshift/commit/d24b3001aaf28fcdb862439df81243c466f83d84))
* **api:** rank a conflict that holds a lane's merge as stopped work ([e9f13ed](https://github.com/bkuberek/ropeshift/commit/e9f13ed908cee4b3cc976211cc5cbfc38193840c))
* **api:** spell the personal setup acceptance route as a literal ([c5066ce](https://github.com/bkuberek/ropeshift/commit/c5066ce56b28d6dda4ab1c7a3d7c273162fd66f0))
* ask about a left-out path once, by its exact name ([caca13e](https://github.com/bkuberek/ropeshift/commit/caca13e9d98035cf71cedfd927b0a6ef1fd8aeb5))
* ask about each plan of a planning branch whose requests at its commit all closed ([3e61fbf](https://github.com/bkuberek/ropeshift/commit/3e61fbf4129f6b05a4e066701d4cc38492d7cc59))
* ask for the passphrase once when registering a new project ([85dfcbc](https://github.com/bkuberek/ropeshift/commit/85dfcbc25c25bcc4afddabaf0bf24c1fb2c98fcb))
* **authority:** refuse lane scrollback to a delegate ([74cd330](https://github.com/bkuberek/ropeshift/commit/74cd3304cd43965e4ac0527c4306ff51f40a181a))
* bring a dialog in a lane's pane to Needs you ([a536dd1](https://github.com/bkuberek/ropeshift/commit/a536dd19df708003acffa0b96318158946cfa456))
* bring an orchestrator whose run ended to Needs you with why ([fdd00fc](https://github.com/bkuberek/ropeshift/commit/fdd00fc5f9163d29872419323259e0ca612ac45b))
* **claudecode:** admit a replay by origin.msg_id only from our reply address ([6d05603](https://github.com/bkuberek/ropeshift/commit/6d05603e0698b25a1cf2fb08ca14eb9c685068f8))
* **claudecode:** answer a trust dialog that drops an early key ([cd8f01f](https://github.com/bkuberek/ropeshift/commit/cd8f01f279b58d1607c073fb714025c90b9592e4))
* **claudecode:** bound a stopping probe and its daemon under the limit init gives them ([fa12f28](https://github.com/bkuberek/ropeshift/commit/fa12f2824877aaf61e71993d65fc4fcf205bbaa2))
* **claudecode:** brief a pane once ropeshift's MCP server has listed its tools ([24bf3a7](https://github.com/bkuberek/ropeshift/commit/24bf3a72b134bac4b7570c1cc428d39c58144d95))
* **claudecode:** clear a probe's older evidence when the next probe begins ([9546e70](https://github.com/bkuberek/ropeshift/commit/9546e706cb206cde90b5de4970c548d58e32dafa))
* **claudecode:** compose a pane's status line with the repository's at the lane's base ([3d260b8](https://github.com/bkuberek/ropeshift/commit/3d260b806f7e5a22c9bf54a1f11665d125942b2c))
* **claudecode:** confirm socket delivery by origin.msg_id ([321f1b3](https://github.com/bkuberek/ropeshift/commit/321f1b31881c33acb8c7ace7571d185e773ad667))
* **claudecode:** confirm socket delivery by origin.msg_id ([6775af7](https://github.com/bkuberek/ropeshift/commit/6775af722fcc4646c86539e14bd5404e34b1145b))
* **claudecode:** confirm typed input against its first Enter ([fefe066](https://github.com/bkuberek/ropeshift/commit/fefe0666eeb638d534977288f2e6286ce6e09494))
* **claudecode:** count typed input delivered only once the session takes it ([bca7228](https://github.com/bkuberek/ropeshift/commit/bca72285ca7f2bbafeeaf9b9fa362e85526908e2))
* **claudecode:** do not follow a link where a probe keeps its evidence ([04e90b8](https://github.com/bkuberek/ropeshift/commit/04e90b8eaeeafbedb66e8d7cc30ec39e4216156f))
* **claudecode:** do not run a failed budget check again on its version ([75751bb](https://github.com/bkuberek/ropeshift/commit/75751bb417e66ef1a57e12939471959f34e428b3))
* **claudecode:** end a stopped probe's pane at once, and close one an open left ([4f743d6](https://github.com/bkuberek/ropeshift/commit/4f743d6333076b57b5a095d9910aae716d42b0f9))
* **claudecode:** end a stopped probe's pane at once, and close one an open left ([4b62ca4](https://github.com/bkuberek/ropeshift/commit/4b62ca4d5c79ed7f64b8a4c77f9d9f62c17d9931))
* **claudecode:** end the shim of a start the daemon cut off ([f16b67f](https://github.com/bkuberek/ropeshift/commit/f16b67faf3b25c0fac434c78c70c587fec1deb18))
* **claudecode:** find nested skills from git's listing, and walk ignored trees by name within a time bound ([ce995e7](https://github.com/bkuberek/ropeshift/commit/ce995e73cbbc0da0a2c193d142af9983d69e3a80))
* **claudecode:** forget the last screen after a failed read in the start check ([ae10d6d](https://github.com/bkuberek/ropeshift/commit/ae10d6d9789c4517760337001e80ac25be1488a5))
* **claudecode:** give a stopped probe's session a short grace before the kill ([2132fc9](https://github.com/bkuberek/ropeshift/commit/2132fc913cbddd4e83ebee528c557d9fab869bf0))
* **claudecode:** give each lane worktree its own skills, agents, and commands directories ([4bf684c](https://github.com/bkuberek/ropeshift/commit/4bf684c44878fd67f8eba3891f0ebf96287f41c3))
* **claudecode:** hold a file the base's commands run, and refuse a start a restore leaves held ([7ae6844](https://github.com/bkuberek/ropeshift/commit/7ae6844c2a4d89767517f9f3913ce1cabb586945))
* **claudecode:** hold a lane on a hook in a skill nested below the worktree's root ([603cff0](https://github.com/bkuberek/ropeshift/commit/603cff0f835f0043d876b50a6b7f38b1ce19e013))
* **claudecode:** hold a lane on any settings key not known to be harmless ([4bea459](https://github.com/bkuberek/ropeshift/commit/4bea4593dbccbf7ea0e914644d468b963bf72bea))
* **claudecode:** hold a lane that adds or changes a plugin under a skills directory ([4bc33d8](https://github.com/bkuberek/ropeshift/commit/4bc33d8c50e1f97bea91a3cd6bd0417576b30890))
* **claudecode:** hold a lane that adds or changes a skill's allowed-tools ([7af6ad3](https://github.com/bkuberek/ropeshift/commit/7af6ad33cd64625a92ef9ba2e5d8d3336b27d1f0))
* **claudecode:** hold a lane that adds or changes a subagent's permissionMode ([1553883](https://github.com/bkuberek/ropeshift/commit/155388393c2f68b1174740353f5fcf85e433c939))
* **claudecode:** hold a lane that changes a file a hook runs ([7a742ca](https://github.com/bkuberek/ropeshift/commit/7a742cac397a6f45cd4ba253ce1a7f2549246953))
* **claudecode:** hold a lane that changes a file its status line or file suggestion command runs ([3429966](https://github.com/bkuberek/ropeshift/commit/34299667b33dfa46bf5c9dca6092cc6d6471f06a))
* **claudecode:** hold a lane whose skill or command body runs shell commands the base does not ([a3dcefe](https://github.com/bkuberek/ropeshift/commit/a3dcefea44a7540cdabc2f2f185b16e0252fa9c6))
* **claudecode:** hold a pane's keys from a prompt in the same millisecond as the stop before it ([3012608](https://github.com/bkuberek/ropeshift/commit/3012608f8063d6f0d2bc77e9d62d5da267287543))
* **claudecode:** hold a resent Enter until a turn the first one may have begun is past its first seconds ([3a89f55](https://github.com/bkuberek/ropeshift/commit/3a89f551c0cb5ca84a94c6dea39e0a43065f761b))
* **claudecode:** hold every worker key while a dialog may be open ([a25c24c](https://github.com/bkuberek/ropeshift/commit/a25c24c730f3cc4e3559166c48667d67dc03266b))
* **claudecode:** keep typed input from a dialog beside a named approval ([117ca9f](https://github.com/bkuberek/ropeshift/commit/117ca9f8cc7444f1fdf6a8b927929cb13f172d36))
* **claudecode:** keep typed input from a question that is open at adoption ([bc3c857](https://github.com/bkuberek/ropeshift/commit/bc3c857cc807a15b24f1ce76375d2d60642e1633))
* **claudecode:** leave out the instructions above a lane's worktree, and report one that loads ([bccf1a7](https://github.com/bkuberek/ropeshift/commit/bccf1a7d5f6f06afc4db76f9065accaed53fe2a1))
* **claudecode:** leave the windows check out of an API key's probe ([6431f94](https://github.com/bkuberek/ropeshift/commit/6431f949ad16eabfab14259e72641e472265bde2))
* **claudecode:** let a lane read its own git directories with no prompt ([1e5ed08](https://github.com/bkuberek/ropeshift/commit/1e5ed08df81b07868c6c6a9d225cfe884069fc05))
* **claudecode:** list a lane's worktree through the git directory its clone records for it ([99e35cc](https://github.com/bkuberek/ropeshift/commit/99e35ccc6df77a3562031b46b53df0213624dbb2))
* **claudecode:** name where a trust dialog's focus went when its Enter did not take ([e2a8417](https://github.com/bkuberek/ropeshift/commit/e2a8417714a19cc7a563673112bdebe2e4064502))
* **claudecode:** open no probe pane once the probe was stopped ([0a37182](https://github.com/bkuberek/ropeshift/commit/0a3718269c50e049213a2b8c07c088f39e4ee8ef))
* **claudecode:** qualify Claude Code 2.1.330 in the shipped matrix ([53921d5](https://github.com/bkuberek/ropeshift/commit/53921d5b0da6a6d2d52d2e1b492286ee2c058b26))
* **claudecode:** read a lane's .mcp.json and status line at its claim's base, never a continued branch's tip ([4e6b4e3](https://github.com/bkuberek/ropeshift/commit/4e6b4e306ccd006d33d6d9ec31a77b6fb6e9a349))
* **claudecode:** read a lane's git directories by their real paths ([a830cb4](https://github.com/bkuberek/ropeshift/commit/a830cb40fe77046c86a793a0a47c7ad36d316791))
* **claudecode:** read a lane's git directories by their real paths ([fe18653](https://github.com/bkuberek/ropeshift/commit/fe1865390a7751b099a751384e3f11f98b701fdb))
* **claudecode:** read no local settings, and the repository's .mcp.json at the lane's base ([33db305](https://github.com/bkuberek/ropeshift/commit/33db3057b33be111d273423105b6f807c73bd789))
* **claudecode:** read the probe's evidence screen through the start check ([a8e24de](https://github.com/bkuberek/ropeshift/commit/a8e24deaa95f19803cb5a2de80cf58014c4bec0f))
* **claudecode:** read the repository's .mcp.json at the base from the clone, through verified objects ([62868b2](https://github.com/bkuberek/ropeshift/commit/62868b25b5ec076c0dafee0dc530b6023f4d4892))
* **claudecode:** read the repository's pieces at the lane's base, and hold a frontmatter that opens and cannot be read ([b79b33d](https://github.com/bkuberek/ropeshift/commit/b79b33d275f9aced05c61fadd2ef21caabfcca2f))
* **claudecode:** rebuild a replayed session's waits from its open approvals ([353730f](https://github.com/bkuberek/ropeshift/commit/353730f492c147bef94ca5545daa2335168c07b5))
* **claudecode:** refuse the bypass mode in a probe's settings ([8970bf0](https://github.com/bkuberek/ropeshift/commit/8970bf0075beed32d0ba6020d6c8729f26663d9c))
* **claudecode:** report a repository MCP server named after ropeshift's own instead of dropping it silently ([63a0ac9](https://github.com/bkuberek/ropeshift/commit/63a0ac93866942cbeb0223dc716aac7e70025fc4))
* **claudecode:** say a hold from a tree too large to walk comes from its size ([5b6c867](https://github.com/bkuberek/ropeshift/commit/5b6c867d353b1fbcead85df29a2ea0de555201ee))
* **claudecode:** say when the trust dialog did not take its Enter ([3fc6f71](https://github.com/bkuberek/ropeshift/commit/3fc6f7157beb6492c8f73a2dac0e192aa7d2962e))
* **claudecode:** start each probe session with a lane's isolation ([98ba549](https://github.com/bkuberek/ropeshift/commit/98ba5495f8caeb95dbb1964cc2f29bbeb0b27ee5))
* **claudecode:** tell the repository's pieces by its .claude directory at the lane's base ([bac0490](https://github.com/bkuberek/ropeshift/commit/bac0490dd49ec743ebd259773169b1f45bba0d8a))
* **claudecode:** turn off in a lane each plugin only the main checkout enables ([ca067a2](https://github.com/bkuberek/ropeshift/commit/ca067a2bab8fc70516fe64088cc90d0318e3509c))
* **claudecode:** use and report the surfaces an adopted session launched with ([42c8096](https://github.com/bkuberek/ropeshift/commit/42c8096c56a18cd66eff6adb75b89007860bdf5c))
* **claudecode:** use the socket only where receipts passed too ([21fd3ac](https://github.com/bkuberek/ropeshift/commit/21fd3acaf8501ede3722a251fb1ad01fad450895))
* **cli:** exit 4 when a lane has no pane or has ended ([68011d2](https://github.com/bkuberek/ropeshift/commit/68011d241ad37c6fcd73cb8842b9ef777b9f01c3))
* **cli:** fetch nothing and allow no transport when new reads a clone ([04a8c96](https://github.com/bkuberek/ropeshift/commit/04a8c96766c7ba8ab5b16a5665825f6f0089349d))
* **cli:** read the clone in a dry run with no hook, and after the git check ([d09011b](https://github.com/bkuberek/ropeshift/commit/d09011be90084a5d4053dea958afd6d387d66a03))
* **cli:** refuse a remote for a folder with a repository in a dry run ([58eea2b](https://github.com/bkuberek/ropeshift/commit/58eea2bb682f63970c8a69bf1c214e9783398ea3))
* **cli:** say why stopped work waits in the plain-text needs list ([589da9e](https://github.com/bkuberek/ropeshift/commit/589da9e5a73e108f9eee01c964507471965f7c24))
* **cli:** take --repository by name in work create and plan start ([a7b038e](https://github.com/bkuberek/ropeshift/commit/a7b038e67884a5bcf3371c99d8a0b3c863a63006))
* **cli:** warn when agent set drops a category from tools.allow ([084f291](https://github.com/bkuberek/ropeshift/commit/084f2911b0677086c45b5365ddf9aa624f477fc8))
* close a waiting branch's request when main records its version with other files ([c027957](https://github.com/bkuberek/ropeshift/commit/c0279575a03532e0ca27f97705d483e1745e3aea))
* close the lane isolation gaps that follow [#414](https://github.com/bkuberek/ropeshift/issues/414) ([c49861a](https://github.com/bkuberek/ropeshift/commit/c49861a67fed0d69ddc4a80c410433f27f1fd76f))
* close the plan approval follow-ups to [#399](https://github.com/bkuberek/ropeshift/issues/399) ([0f620ed](https://github.com/bkuberek/ropeshift/commit/0f620edfec68bf24d8333717bc6837d47ba3dcde))
* **control:** ask for a planning branch's merge when its commit holds a plan the ledger holds ([ffb27cb](https://github.com/bkuberek/ropeshift/commit/ffb27cbb450933cea7c9c1739c56ac77e1d2e9a6))
* **control:** ask for no orchestrator before the first passphrase is set ([ef4f5b2](https://github.com/bkuberek/ropeshift/commit/ef4f5b2ef9a470d44b09c7214222dc5228207ecb))
* **control:** bind a plan approval to the commit its request named ([fd32999](https://github.com/bkuberek/ropeshift/commit/fd3299929b40899a34787c655aaf47a08606dbfb))
* **control:** count no forced interruption for the start of an ended run ([43df3d2](https://github.com/bkuberek/ropeshift/commit/43df3d2fd6b0abaa9cc93ddb2112a40b89030f02))
* **control:** count running consultations against the project's limits and refuse a reused target ([3fca107](https://github.com/bkuberek/ropeshift/commit/3fca107466fae78fd2a4d68509ebc8c9c3a6f5ad))
* **control:** count the start of a run that ended on a failure as a forced interruption ([60caacd](https://github.com/bkuberek/ropeshift/commit/60caacd7ee8bcc49ddb5aee2cce39994b909ae9c))
* **control:** hold a running consultation's budget whatever becomes of its question ([6ba0692](https://github.com/bkuberek/ropeshift/commit/6ba0692fa5b3562ade547a6ec5b9d35ee0a460fb))
* **control:** leave a forge rebase's landed range out of a person's pushes and show the pushed side of a rewriting repository ([5bde913](https://github.com/bkuberek/ropeshift/commit/5bde91354f6b143130128cc3c448d686221e9085))
* **control:** let an unattended orchestrator decide the choices the policy leaves it ([343132c](https://github.com/bkuberek/ropeshift/commit/343132c12c66c2b499e12b5c1d73a4073a42cdbc))
* **control:** name the plan work order when a held version is asked about again ([89ed71d](https://github.com/bkuberek/ropeshift/commit/89ed71d3de7d912f32bd8563aefd36472275b6cb))
* **control:** never offer a placeholder plan version for approval ([1148879](https://github.com/bkuberek/ropeshift/commit/11488794d9810e9172b6e4cc5940e4878ef7bb7f))
* **control:** offer a held plan version when its request's work order is gone ([e30a7a1](https://github.com/bkuberek/ropeshift/commit/e30a7a17fb23f0711084cf0fd2d85d6a9f504751))
* **control:** read a worker's one-way history by event ID instead of scanning the log ([ecaf6ba](https://github.com/bkuberek/ropeshift/commit/ecaf6bad2598854d80305b85c44339886bb2463e))
* **control:** record what a lane's session loaded as bounded plain text ([80989c0](https://github.com/bkuberek/ropeshift/commit/80989c0f1b88fef315ad2b947f9fea5e3106b5be))
* **control:** refuse a message of kind answer that a sender makes ([d88346c](https://github.com/bkuberek/ropeshift/commit/d88346cea1fbacef0864e5e527a72aade3fa6cb6))
* **control:** refuse a message to an orchestrator whose run ended ([3c2872e](https://github.com/bkuberek/ropeshift/commit/3c2872e1cc011c1359f34978b13bf6858f185f75))
* **control:** refuse the reset of a lane that has not reported as a move ([7b53a0e](https://github.com/bkuberek/ropeshift/commit/7b53a0eeffba2a7ab61f652e5b5e7cf5df79e56b))
* **control:** resume a woken lane on the agent the roster picks when its pinned agent no longer fits ([f8bfcf2](https://github.com/bkuberek/ropeshift/commit/f8bfcf2de6680695d876b2e0236ca9fe9f013d2c))
* **control:** start the next run of an orchestrator whose run ended ([44d24da](https://github.com/bkuberek/ropeshift/commit/44d24da35daa1a6dfe57cedc7f38d3cef599a85b))
* **control:** stop telling an ended run it starts at the reset ([81b182d](https://github.com/bkuberek/ropeshift/commit/81b182d8d0e4e5d9eeda4d9f8c84ccd149a2de32))
* disable every configured git hook in merges and the commits the worker makes ([4c0c8c3](https://github.com/bkuberek/ropeshift/commit/4c0c8c391c921b30a7d67301ab93910ba40621fe))
* **entry:** bound the wait on the daemon ropeshift init starts ([c21244e](https://github.com/bkuberek/ropeshift/commit/c21244ea8627f93cc7856ee229a2c324e0fa1ba9))
* **entry:** count a daemon source as a checkout of the first project ([05eefa3](https://github.com/bkuberek/ropeshift/commit/05eefa368622d4c9c567fdb897aa76a1213fbfeb))
* **entry:** keep every daemon entry point's children from inheriting descriptors ([dfe5c5d](https://github.com/bkuberek/ropeshift/commit/dfe5c5da8376becd8910527234b6660a0630f736))
* **entry:** keep the daemon ropeshift init starts from probing the agent CLI ([9d811b7](https://github.com/bkuberek/ropeshift/commit/9d811b755c9d725d722ffc537c3157feef593dcd))
* **entry:** keep the mark of init's daemon from what the daemon starts ([db5ea6c](https://github.com/bkuberek/ropeshift/commit/db5ea6c2ccfdc2ff5aa00047039852cf93677481))
* **entry:** keep the owner's updater pin in a service's environment ([cc7cafd](https://github.com/bkuberek/ropeshift/commit/cc7cafde54713761e867d24830b668c08d307607))
* **entry:** leave the solo daemon's hosts alone for a source git cannot read ([3ee71c1](https://github.com/bkuberek/ropeshift/commit/3ee71c155a645b2b04910366682f8a04567c5bc5))
* **entry:** make the daemon init starts internal, and keep it from running a session ([0bb1cde](https://github.com/bkuberek/ropeshift/commit/0bb1cdedfc89785dbc77e587bd11ba8ee722b24c))
* follow up on the orchestrator consult ([388e576](https://github.com/bkuberek/ropeshift/commit/388e5767d708a8da0043fc27d76929353ec493a2))
* follow up the one-way decisions and their withdrawal ([4636fe1](https://github.com/bkuberek/ropeshift/commit/4636fe1ac7589fde84e322ce1e43838c78358ded))
* **ghcli:** keep the daemon's secrets from gh ([ac1a25c](https://github.com/bkuberek/ropeshift/commit/ac1a25ca6886e8903c5daafd173d7b3ddfa004e9))
* **gitcli:** carry the work-order trailers on every commit of a rebase merge ([a1d91d9](https://github.com/bkuberek/ropeshift/commit/a1d91d905a2eddcb5ead9f0c7bf2be2ecb931435))
* **gitcli:** keep the daemon's secrets from git and run no hook in a worktree add ([8fcb209](https://github.com/bkuberek/ropeshift/commit/8fcb209cb53f5f45dade6c97276af2e1ea1262c7))
* **gitcli:** run no repository hook in a create's fetch or a lane's fetch ([f126e45](https://github.com/bkuberek/ropeshift/commit/f126e45249c135c4ea8f4f23198efc1929a38300))
* **gitcli:** run no repository hook in FetchCommit and disable the unnamed hook ([200ed34](https://github.com/bkuberek/ropeshift/commit/200ed34099f5b946b9a2abe03f69a4a6624553ae))
* harden text typed into panes, lane secret references and webhook signing ([9311f40](https://github.com/bkuberek/ropeshift/commit/9311f40f3e89bdb5babc9b76e6efdc58c6353908))
* hold a consultation's budget until its end arrives ([d5ca8be](https://github.com/bkuberek/ropeshift/commit/d5ca8be3a626ce8be4eb9e36685b56b265426c75))
* hold a planning branch's merge for each plan whose requests at its commit all closed ([e13b3ca](https://github.com/bkuberek/ropeshift/commit/e13b3ca9fcfb7683e69ea1940d0db94b9fc3e2c0))
* **hook:** ask for a lane's read outside its worktree and read-only trees ([7d772c3](https://github.com/bkuberek/ropeshift/commit/7d772c38a4661723b971a55f3dd2e6a962d9227f))
* **hook:** ask for a lane's read through a link, and read more of what a lane's shell reads ([840cf79](https://github.com/bkuberek/ropeshift/commit/840cf793118f6173dfd1b63ebe5ba09148661513))
* **hook:** ask for a link, an inline script's files, and the reads that stay ordinary ([2f53f56](https://github.com/bkuberek/ropeshift/commit/2f53f5637a2317ff323a47617ff3676bda2bc774))
* **hook:** ask for an xargs call that reads its arguments from a file ([f74197c](https://github.com/bkuberek/ropeshift/commit/f74197ccacf34619071af0fcd19bfdf5297a3778))
* **hook:** ask for every form of a push and a gated command ([e839b59](https://github.com/bkuberek/ropeshift/commit/e839b59c22eef09a80e575689a2e60cdc1f3014b))
* **hook:** ask for every reader that xargs feeds or a variable names ([b5b0266](https://github.com/bkuberek/ropeshift/commit/b5b0266894bb311b06d35e9fdbd5d92f14aeb562))
* **hook:** block a reload when its hook panics, and test that a timeout blocks it ([c056aa4](https://github.com/bkuberek/ropeshift/commit/c056aa4cad53b79d814251e610a54d463803ba23))
* **hook:** fail closed on a varying word of a gated program ([0ff33af](https://github.com/bkuberek/ropeshift/commit/0ff33af638d75ced1c5908d54cf2ad4489c6d81e))
* **hook:** keep an xargs argument file from hiding behind another option's value ([0abdf9c](https://github.com/bkuberek/ropeshift/commit/0abdf9cc13bf5f95fcace0a22cbf89788bae7397))
* **hook:** read every word of an xargs call for an argument file ([2301ac7](https://github.com/bkuberek/ropeshift/commit/2301ac7eb1eb2482c8ef6251209371491e14016c))
* **hook:** read wrappers' operands, options' values, and Monitor commands ([1e2f0e0](https://github.com/bkuberek/ropeshift/commit/1e2f0e089e035e4b5b1ff9807ca38a7c68da7efb))
* **hook:** shrink the reads a lane's shell may do unasked to what an allowlist proves ([9c9c395](https://github.com/bkuberek/ropeshift/commit/9c9c395c0b1035d32eb104489c3c6e645aa530e5))
* **init:** harden the daemon ropeshift init starts ([ad5a1b7](https://github.com/bkuberek/ropeshift/commit/ad5a1b7d79f22e79d7b18e9c8592ddb089011a40))
* **init:** keep the daemon's log when ropeshift init fails, and say where it is ([f8393ac](https://github.com/bkuberek/ropeshift/commit/f8393acf4edc79c9a60e149780840d7ca0948ba0))
* keep a decision's question and refuse a second keep once the kept decision ended ([4c2ff19](https://github.com/bkuberek/ropeshift/commit/4c2ff19d7a851dc45742fa6d8772a3547d54e396))
* keep the daemon's secrets from git children and lane references ([730c5dc](https://github.com/bkuberek/ropeshift/commit/730c5dc8e03f78c6d32300ad125bab7b5e834a54))
* keep the planning branch a held plan version's request names ([c6174c6](https://github.com/bkuberek/ropeshift/commit/c6174c6925b4bca09285f266e5596c01bdcea326))
* **lanes:** ask for the outcome the work reached when background work outlasts the wait ([67c7456](https://github.com/bkuberek/ropeshift/commit/67c7456976fd4581a9e263cebec185afa8f4a7c7))
* **lanes:** compare a lane's own settings with its claim's base, never a continued branch's tip ([9a7f54b](https://github.com/bkuberek/ropeshift/commit/9a7f54b4c5d2df5b6717d74432379980c119e917))
* **lanes:** defer a gated call a bare wildcard matches ([cc6a7c9](https://github.com/bkuberek/ropeshift/commit/cc6a7c967da6518e151a7abcd54e4ac5a8438d30))
* **lanes:** do not ask for time -p -- or for nice's adjustment word ([88e576a](https://github.com/bkuberek/ropeshift/commit/88e576ab4cf75f96c1d4b8c2673a1b1265f542ff))
* **lanes:** hold each layer to the permission mode below it ([1273bb4](https://github.com/bkuberek/ropeshift/commit/1273bb4500af294dcca28a04b39a76cc0747c090))
* **lanes:** let a lane keep the owner's DISABLE_AUTOUPDATER ([ea55863](https://github.com/bkuberek/ropeshift/commit/ea55863b365d67f1c12dd6af174bc93c9dde7e87))
* **lanes:** offer no restore for a hold from a script a hook runs ([c7ceb38](https://github.com/bkuberek/ropeshift/commit/c7ceb385e80a55a0d28de2519a186fa086aeba56))
* **lanes:** read a run of open brackets in linear time ([c00a09c](https://github.com/bkuberek/ropeshift/commit/c00a09c16ed0093b078fbf827c077dc7bf183470))
* **lanes:** read parallel's command past the values of its options ([ba85574](https://github.com/bkuberek/ropeshift/commit/ba8557498db912f3a4a3863b1c4423504b0f29de))
* **lanes:** read sudo -h and env -P both as taking a value and as taking none ([9172863](https://github.com/bkuberek/ropeshift/commit/91728631149b7028ce0df3075d6c85f499561efc))
* **lanes:** read what parallel, watch, trap, and su run in the holds ([c8ca8af](https://github.com/bkuberek/ropeshift/commit/c8ca8af603f260d950fe3c8fec2ff31998c3ba46))
* **lanes:** recommend the separate directory for any leaked piece that adds more than context ([a08587b](https://github.com/bkuberek/ropeshift/commit/a08587bd931ecdd760af2fa6c6a74baca65fc39c))
* **lanes:** refuse a command word that expands on a lane that only reads ([ba869d7](https://github.com/bkuberek/ropeshift/commit/ba869d7b02b6eac94d4706571d15f7fbf7026b92))
* **lanes:** refuse a profile secret reference to a ROPESHIFT_ variable ([1d9092a](https://github.com/bkuberek/ropeshift/commit/1d9092a80ccfa9f4a37368df4373d58c3bd4521b))
* **lanes:** refuse an env reference to a secret of the daemon ([1a95173](https://github.com/bkuberek/ropeshift/commit/1a951737d81a35459006018c804314bf79934bc6))
* **lanes:** state an answer to the lane's own question as in force ([6c47986](https://github.com/bkuberek/ropeshift/commit/6c47986e3058bd8788e7fe9a448c8da3e86b9519))
* **lanes:** state both conditions of an unattended one-way choice in the role ([8f684bc](https://github.com/bkuberek/ropeshift/commit/8f684bc21216c01136e1ebfc3427412995fa827e))
* **lanes:** tell an unattended orchestrator to decide what the policy leaves it ([3483983](https://github.com/bkuberek/ropeshift/commit/348398379ab4e86163a45440827a8e79923355f6))
* **lanes:** withhold the older webhook key and the database password ([fde7ae7](https://github.com/bkuberek/ropeshift/commit/fde7ae7931796917c936bf54240568de52ec570e))
* **ledger:** bound the model a consultation's end carries ([8dca051](https://github.com/bkuberek/ropeshift/commit/8dca051a6b603c3a5aa4eeefec195142121363f2))
* **ledger:** keep an ended orchestrator run ended ([1e71ee1](https://github.com/bkuberek/ropeshift/commit/1e71ee1cf24f19754af128b8ec536c2b3f847cca))
* **ledger:** keep why an orchestrator's run ended ([82eed24](https://github.com/bkuberek/ropeshift/commit/82eed24acf2ef4a3501c9e90e9d8e924ae51e6d8))
* **ledger:** say a matrix lane's surfaces change at a respawn, not when the check ends ([b4cb5a1](https://github.com/bkuberek/ropeshift/commit/b4cb5a1dfe70868ac0bb90331a630f1951e6621e))
* list the contracts a plan version held ([89d014c](https://github.com/bkuberek/ropeshift/commit/89d014c7156d82bf20588955870b9ea957729da4))
* **messages:** state a live answer to the lane's own question as in force ([abd14ed](https://github.com/bkuberek/ropeshift/commit/abd14ed6775b3af0c998f1710afad1f9f43d7451))
* name a planning branch on a held plan's request only when its commit changed the plan ([75142b0](https://github.com/bkuberek/ropeshift/commit/75142b0ba6af7462c8136fe26b1748da3f93c6d5))
* name each Keep button by its decision ([b916854](https://github.com/bkuberek/ropeshift/commit/b916854fc3bddf5670b45af361bfdd4258929ba6))
* name main on a plan card whose version main's request also asks about ([a0d9994](https://github.com/bkuberek/ropeshift/commit/a0d9994cdccfec19c71f4d9bfb59b47154550d45))
* name the surfaces a lane's process actually started on ([aeac99c](https://github.com/bkuberek/ropeshift/commit/aeac99c027d77766fac70fca3c6d6f40af111f97))
* **needs:** keep a one-way decision in the project its item names ([8f0b6dc](https://github.com/bkuberek/ropeshift/commit/8f0b6dc8879498dd86815ee97746c12b68ff48ba))
* **needs:** read the recording events of one-way decisions once ([6642dd8](https://github.com/bkuberek/ropeshift/commit/6642dd87edc05c3ced8dbf3e833a4afdd69ce5fb))
* **notify:** fail a webhook delivery whose key variable holds no key ([99f90aa](https://github.com/bkuberek/ropeshift/commit/99f90aadf872f2b171b599fec3c4ffd505195bf3))
* **orchestrator:** answer a consult call sent again with the grant it first got ([5befc1b](https://github.com/bkuberek/ropeshift/commit/5befc1bd9bca69e3cbf09d4def2cf9e1180ee682))
* **orchestrator:** cite a question's consultation on every answer that closes it ([e9f29b4](https://github.com/bkuberek/ropeshift/commit/e9f29b451651ef398c03ef91c184d39e12a21162))
* **outside-edits:** hold back a range-less rebase's pushed side, keep messages as written, and read nothing beneath a link ([a9973dc](https://github.com/bkuberek/ropeshift/commit/a9973dcf508e99dbee8ab9a0bc3156e45edf4ae9))
* **outside-edits:** read links, submodules, and odd paths without stalling, keep a refused clone, and read again fresh ([ed4b1ff](https://github.com/bkuberek/ropeshift/commit/ed4b1ffb529ed9b660c44edc6d66a7b3fa71c8ed))
* paste into a pane that holds the keys and add Ctrl-D to the key row ([4c5c1aa](https://github.com/bkuberek/ropeshift/commit/4c5c1aaa6de92d21a005122d0ef2282738d10c26))
* probe a version again when a check did not pass, and keep the evidence ([4ee06e8](https://github.com/bkuberek/ropeshift/commit/4ee06e843512883346a06458e4f4d651096690e9))
* **profiles:** make auto the built-in permission mode ([7a19a85](https://github.com/bkuberek/ropeshift/commit/7a19a85619b6b2f7559e291b22c9f286f57d5f9e))
* **profiles:** make auto the built-in permission mode ([51d32a3](https://github.com/bkuberek/ropeshift/commit/51d32a365119355f5ba1adbf48dcb8f8fdb47c5a))
* **profiles:** read the repository's layer only through objects that hash to their names ([a742dcc](https://github.com/bkuberek/ropeshift/commit/a742dcc3cfd39265464eb87463f3e8a9996dbb0a))
* **questions:** keep security, compliance, and data in every decision policy ([c00b9d8](https://github.com/bkuberek/ropeshift/commit/c00b9d83e0abeae718d12f0bed4ed1532f9f22a4))
* **questions:** let an unattended orchestrator escalate a one-way choice from any rung ([2630a0a](https://github.com/bkuberek/ropeshift/commit/2630a0a2ed0b0cf6b19457c7ffbb6e8df3c1ad59))
* read a lane's start once and file by file, and keep what it leaves out ([a70e4f6](https://github.com/bkuberek/ropeshift/commit/a70e4f62940d00a76bc6c35bfa61f0817df2cc0f))
* read a left consultation's session before stopping it ([f5fb794](https://github.com/bkuberek/ropeshift/commit/f5fb79445a80d17dc766c35cf25f1aec44d0591d))
* read the commit a plan request names when approving accepts its work ([23de625](https://github.com/bkuberek/ropeshift/commit/23de62516c47bd2f92f97291f08e13c13df11e9a))
* read the files of a lane's commits NUL-separated ([3450b09](https://github.com/bkuberek/ropeshift/commit/3450b09a01b71d3226e1dad382b57c52ca1e7f35))
* read where a landed merge ends from every commit, and fail on a merge-base git cannot answer ([8745cb8](https://github.com/bkuberek/ropeshift/commit/8745cb8b1e03664e1ef04b3ac74ec78379221675))
* rebuild an adopted session's state from what a restart can know ([cd13902](https://github.com/bkuberek/ropeshift/commit/cd139020dcb7b13a2fa025b1a3129ab63e07c3b1))
* redact the detail of a canceled lane's report ([48f33f5](https://github.com/bkuberek/ropeshift/commit/48f33f5a9903f0aeff1567067588052b97dfef62))
* refuse a lane resume that wakes nothing and a reset of a lane that did not trip ([85ab15d](https://github.com/bkuberek/ropeshift/commit/85ab15de45309066ca4cbea17c9dba2412a9c6b5))
* replay a rebase merge's messages verbatim whatever commit.cleanup says ([6ef3dca](https://github.com/bkuberek/ropeshift/commit/6ef3dcae8091cd2866fc07cf828fc35a4db7553b))
* **roster:** never run a lane as the consultant ([171ece5](https://github.com/bkuberek/ropeshift/commit/171ece51b4881d5b4066376d14aa6af64565f732))
* S1 findings ([4cb24a3](https://github.com/bkuberek/ropeshift/commit/4cb24a36a2feb4e82073d4633b91741cb027006c))
* S1 rerun findings ([9a90c7c](https://github.com/bkuberek/ropeshift/commit/9a90c7cccab3b6e4127bc0319946a78eccbfd118))
* say what to change when a webhook address is refused ([3f99c40](https://github.com/bkuberek/ropeshift/commit/3f99c400ded60c1275a61fdcaeec99e27b5d563e))
* say when an old plan request did not record the plan's goal or contracts ([0d477ce](https://github.com/bkuberek/ropeshift/commit/0d477ce633f6e38858e2718e25a5e85efbbbabdf))
* say why a push went unchecked, and read a publish in its checkout ([9f3f170](https://github.com/bkuberek/ropeshift/commit/9f3f17084427f03c47e57724ccc2bb481a204ac1))
* send a planning branch back for rework when a member denies one of its plans ([9ae301c](https://github.com/bkuberek/ropeshift/commit/9ae301ce8d0e4332f73f96b9ac297a57164d87b0))
* send a two-plan branch back when main records the version of one plan ([ca8bb44](https://github.com/bkuberek/ropeshift/commit/ca8bb44269730021cf046b67bbde77db248f0187))
* send a waiting planning branch back for rework when main changes its plan ([ced73a6](https://github.com/bkuberek/ropeshift/commit/ced73a6ac79e806dd96f84794e275d2318a7dca7))
* **sqlite:** keep a clear when the cleared session starts again ([d6e82c9](https://github.com/bkuberek/ropeshift/commit/d6e82c96a83d887bc7f57112ee563d469d8905c8))
* **sqlite:** name the installation migration the orchestrator end pairs with ([32e199d](https://github.com/bkuberek/ropeshift/commit/32e199d16cd972ba07fe79e2f2854f9e7ae89f21))
* **sqlite:** number the orchestrator end migration in the installation set ([4e17e63](https://github.com/bkuberek/ropeshift/commit/4e17e63fdce686eeb1d8ca2e7eadb07d74a73461))
* **sqlite:** read a tool call still pending at the gate column's arrival as a gate ([2184a65](https://github.com/bkuberek/ropeshift/commit/2184a6586f93ed9461e6e25ce738e29571a1ea88))
* take the spool before a first prompt reads a lane's start ([2232bd0](https://github.com/bkuberek/ropeshift/commit/2232bd073522869e3463ec060a03c9944377fa31))
* tell work ropeshift landed from a person's push when merges rewrite commits ([6f8aa56](https://github.com/bkuberek/ropeshift/commit/6f8aa569358051ee13d88c04b40b9f8407c702a8))
* **tmux:** send only printable text and keep a trailing semicolon ([c4dd42a](https://github.com/bkuberek/ropeshift/commit/c4dd42ad5c0138a87241e10ed2a006ffc0ea6d16))
* wait for a lane's own background work before judging its turn ([586ed2b](https://github.com/bkuberek/ropeshift/commit/586ed2bea9ff1ac0710b732ad75ba306217ba187))
* **web:** bound the planning page, so its pane scrolls in its screen ([cd277a2](https://github.com/bkuberek/ropeshift/commit/cd277a252d07292b7c7cb32dd8cb41dcae152d86))
* **web:** group a lane's items in Needs you by their project and their lane ([f030998](https://github.com/bkuberek/ropeshift/commit/f03099835e0878587bb1e7cc0d7541276ca1138e))
* **web:** keep the member's place while the scrollback loads, beside the pane's fitted screen ([13de298](https://github.com/bkuberek/ropeshift/commit/13de298fb8a1e7e02c9a50238a3c4d3ed24a4931))
* **web:** leave focus where a member works outside Needs you when it moves on ([b561b9b](https://github.com/bkuberek/ropeshift/commit/b561b9b76fa8c3605eeace2f645c35e81071c293))
* **web:** offer Stop for an orchestrator whose run ended ([e66df86](https://github.com/bkuberek/ropeshift/commit/e66df8614dd03d5d97d3e389e5ad21df693d0d44))
* **web:** record a one-way answer only through its confirm ([c82968d](https://github.com/bkuberek/ropeshift/commit/c82968d403c22f9f64ef84985ada54787615bde1))
* **web:** Retry needs no typed reason ([92940f4](https://github.com/bkuberek/ropeshift/commit/92940f4b83d986b81bb4c7f911b352176c6ad52c))
* **web:** scroll a lane's pane in its screen beside the outline and in the panel ([da8794f](https://github.com/bkuberek/ropeshift/commit/da8794f909b93854e25dc5d1b03fee682b923590))
* **web:** stay on work retried in Needs you, so it offers Start ([430af79](https://github.com/bkuberek/ropeshift/commit/430af79ce2a15a9916efe217041432f1494eba1b))
* **web:** tell Needs you of a retry from its reply, before the read after it ([4b60dc4](https://github.com/bkuberek/ropeshift/commit/4b60dc443215a483a00e0d8024c5ae1330167282))
* **worker:** ask for no work order's facts in an orchestrator's next generation ([5bc4374](https://github.com/bkuberek/ropeshift/commit/5bc4374acf16f7171b4c61e2d4f8c5b5a4ce07bf))
* **worker:** bound a turn no end was heard for, and a rest a restart took over ([19a9262](https://github.com/bkuberek/ropeshift/commit/19a9262e6876b6faa04e1863d750fa94920c5d90))
* **worker:** bound the wait on a refused nudge ([c1fdc4e](https://github.com/bkuberek/ropeshift/commit/c1fdc4efc274e2134cb0feeccdd1e15b183f6464))
* **worker:** bound the wait on a refused nudge ([a1aaf51](https://github.com/bkuberek/ropeshift/commit/a1aaf51fad4f399eeb1d02ddc9be78eeda028796))
* **worker:** continue a waiting orchestrator when a restart finds the fleet draining ([da42acf](https://github.com/bkuberek/ropeshift/commit/da42acfb12a3745182625d5549babee768d325c8))
* **worker:** count a turn's end before reading what its generation owes ([14e7060](https://github.com/bkuberek/ropeshift/commit/14e70605b1f6090354b1e0bbaad2bf23f4a1e6db))
* **worker:** date an old limit at the hook that found it ([9dd3f0e](https://github.com/bkuberek/ropeshift/commit/9dd3f0e40582d3a83e839168c674bd0035d98eda))
* **worker:** date an unheard usage limit at its hook ([ed6a2a9](https://github.com/bkuberek/ropeshift/commit/ed6a2a9de793ba74f0e81871afc82f4a70e12672))
* **worker:** decide each turn's end by the Stop of its own turn ([01b3df4](https://github.com/bkuberek/ropeshift/commit/01b3df4ee8ad4e3b354ca6f26eb345f9ba604f20))
* **worker:** don't nudge a lane whose background work started the next turn ([aeccc3c](https://github.com/bkuberek/ropeshift/commit/aeccc3c70388636593502fc9d2d1be2675bea6ba))
* **worker:** don't nudge a lane whose background work started the next turn ([2057815](https://github.com/bkuberek/ropeshift/commit/2057815e694a742cac3cb5a3b23d41b724e59514))
* **worker:** end a consultation the journal cannot keep instead of leaving its hold to run out ([932312f](https://github.com/bkuberek/ropeshift/commit/932312f4f3e2ac18775d0c9d70d89c16d83ac5b2))
* **worker:** end a stopped orchestrator awaiting its wake as fenced ([2e9c60d](https://github.com/bkuberek/ropeshift/commit/2e9c60d443498c931cdc75e6971e8f7a18430541))
* **worker:** end turn waits with their process, and land only taken wakes ([731e5d5](https://github.com/bkuberek/ropeshift/commit/731e5d55a004e102cd4c3ed6d72d93de4ea66485))
* **worker:** finish a park begun during an adoption after it ([50b472b](https://github.com/bkuberek/ropeshift/commit/50b472b424204fe3538b11e49b101e1f6df8bace))
* **worker:** go on with the held result's sign-in while the sign-in is unread ([d0a4424](https://github.com/bkuberek/ropeshift/commit/d0a4424062efafc84d9c432d100dcf2885f420d0))
* **worker:** hand the driver the dialogs and prompts heard during an adoption ([0d6224d](https://github.com/bkuberek/ropeshift/commit/0d6224d5a3816e00af2a847a727a82f3dd410272))
* **worker:** hold a result only if it passed every check a probe runs now ([100acea](https://github.com/bkuberek/ropeshift/commit/100acea66168d2b064007f3d8665a275e6d7ac8d))
* **worker:** hold the tools of a mid-turn park once its session is adopted ([cf55860](https://github.com/bkuberek/ropeshift/commit/cf5586017e8dafb6e62558d8f4e3bd94bc0eeefb))
* **worker:** journal a consultation's start so a restart ends what a worker left ([7ceeaec](https://github.com/bkuberek/ropeshift/commit/7ceeaecf33b78e92bc61ae9189b4f9434ac21908))
* **worker:** keep a bounded wait armed until its bound decides it ([b0af351](https://github.com/bkuberek/ropeshift/commit/b0af351eee44c8e1c7bf39cb3f0b3d8d5c3e1010))
* **worker:** keep a brief in progress when its prompt moved the lane first ([941e856](https://github.com/bkuberek/ropeshift/commit/941e85654c54d530e547bfce83e81f6c2c4703f3))
* **worker:** keep a brief's turn end that arrives before its hand-off ([f9a70a9](https://github.com/bkuberek/ropeshift/commit/f9a70a9bd1d5fa15bea74784bc8405ed85290f73))
* **worker:** keep a brief's turn end that arrives before its hand-off ([fce8a4a](https://github.com/bkuberek/ropeshift/commit/fce8a4a17760857c2c60b1ea3615955fba1e9263))
* **worker:** keep a lane's declared wait when it is declared ([5d475b6](https://github.com/bkuberek/ropeshift/commit/5d475b6642dd698a561a0fae136e2caedb67a633))
* **worker:** keep a lane's wait, day verdict, and model across a restart ([1e84175](https://github.com/bkuberek/ropeshift/commit/1e8417534233b99050c81dba06de0f3b418a72d0))
* **worker:** keep a planning lane's session while its background work runs ([482f925](https://github.com/bkuberek/ropeshift/commit/482f925acf57c63d37cb7488ab1751075b9c9c36))
* **worker:** keep a probe's attempt at its end before the next probe may begin ([1391346](https://github.com/bkuberek/ropeshift/commit/13913465077964498a3b7af42af66b18e81ab76e))
* **worker:** keep a probe's attempt with the qualification lock let go ([dd265b2](https://github.com/bkuberek/ropeshift/commit/dd265b23a6da9c221b143c88616444389e8a66db))
* **worker:** keep a request count a live hook raised through a journal reread ([f9c5e1f](https://github.com/bkuberek/ropeshift/commit/f9c5e1f51d0c05c080563ec1accca3460da5a092))
* **worker:** keep a running lane's probe-added condition through a probe of another version ([69a7598](https://github.com/bkuberek/ropeshift/commit/69a75988ff0fe153134a896089cb84844f63370f))
* **worker:** keep a turn end's progress when the worker stops after the rest ([2626323](https://github.com/bkuberek/ropeshift/commit/262632313ad33619726b31236bb74023e81f1708))
* **worker:** keep a version's probe backoff across a worker restart ([4fb0a34](https://github.com/bkuberek/ropeshift/commit/4fb0a3415171af40168bc2d096112bc83b72b285))
* **worker:** keep matrix surfaces off an adopted lane that ran on a probe's answer ([0fb66e6](https://github.com/bkuberek/ropeshift/commit/0fb66e67c551f1d30748ff4538ce8150c48cd3f4))
* **worker:** keep no background count for a Stop in the newest prompt's millisecond ([d92221f](https://github.com/bkuberek/ropeshift/commit/d92221ff4beefdea8e4fb8be5020cbf2e601ccdb))
* **worker:** keep the repository's gates for a review with no recorded base, and run none of its hooks ([5d82817](https://github.com/bkuberek/ropeshift/commit/5d828179485a607f89c17cb1860bb03b56b4aff7))
* **worker:** keep the result in force when a probe cannot run ([3e0ff44](https://github.com/bkuberek/ropeshift/commit/3e0ff44b73767ca6042557c950dbdbf2d2a2eec5))
* **worker:** keep the worker of the daemon ropeshift init starts from taking any work ([a659d28](https://github.com/bkuberek/ropeshift/commit/a659d280e76b3353ade6d7472196c48e6dbb1ca2))
* **worker:** keep typed input out of a dialog that is open at adoption ([53fa31e](https://github.com/bkuberek/ropeshift/commit/53fa31e11cdb05b660d97f703995a973fbe5278f))
* **worker:** keep what a consultation's session cost before its worker went away ([c347e87](https://github.com/bkuberek/ropeshift/commit/c347e87b0c8f1871ad7f6b555cf6a236dc46d62f))
* **worker:** leave a lane the daemon stopped while it provisioned for the next worker ([7fcae44](https://github.com/bkuberek/ropeshift/commit/7fcae44c8140f9564ccfa521fdf0b84c08f99f5f))
* **worker:** leave a lane working through an end older than its newest prompt ([1edf069](https://github.com/bkuberek/ropeshift/commit/1edf069a9aa00528227f0978f40eab4c6a04dfd6))
* **worker:** leave a newer compaction standing at an end older than the newest prompt ([3227a1d](https://github.com/bkuberek/ropeshift/commit/3227a1deb79afd67087943e44e48bee7d811c3cb))
* **worker:** let a lane report blocked once its question is dismissed or merged away ([5986e6f](https://github.com/bkuberek/ropeshift/commit/5986e6fdb9cc0b79e7756e8fbbcdbfa64f525079))
* **worker:** let a late turn's end discard only what is older ([d508d7f](https://github.com/bkuberek/ropeshift/commit/d508d7fb3ad559d824a46d014b06b37a4d1a0e54))
* **worker:** let a Stop in the newest prompt's millisecond end no turn ([a530536](https://github.com/bkuberek/ropeshift/commit/a53053686db2c80a7c8ccf43e32fc60c775b4ed5))
* **worker:** let an adopted assistant post its replies ([4b06b9d](https://github.com/bkuberek/ropeshift/commit/4b06b9dab9f25ddf77e6ba42edab478a3c51742c))
* **worker:** load an orchestrator's profile when a restart continues its run ([a4f2cba](https://github.com/bkuberek/ropeshift/commit/a4f2cba04b6455d7c7385dc2e49dd64ecdadf1ec))
* **worker:** look at the installed version when a probe's end is kept ([b0d1992](https://github.com/bkuberek/ropeshift/commit/b0d1992f94bbe5b3a0fdb13cebe724c7152a8f1c))
* **worker:** look for an assistant's session in its conversation's directory ([ade9b3f](https://github.com/bkuberek/ropeshift/commit/ade9b3ff6e417edee2037e4742c9426d9e90f004))
* **worker:** look for an orchestrator's session in the private directory it runs in ([1482bd5](https://github.com/bkuberek/ropeshift/commit/1482bd50fc6da79444dc6d254bccd438166e70f1))
* **worker:** mark a request a gate when any command of its shell command is one ([f47ae4d](https://github.com/bkuberek/ropeshift/commit/f47ae4d84b3a7ce9cfcfaefb46ae8ea51c51c367))
* **worker:** name a failed settled publish, and count only those in the away-plane test ([51930f6](https://github.com/bkuberek/ropeshift/commit/51930f61a1ea75b75d5b842c286ce81d612820fa))
* **worker:** name the real reason a qualified version's temporary no channel left a message undelivered ([fd9c09d](https://github.com/bkuberek/ropeshift/commit/fd9c09db7bfb0dd6d016003efbe4fac0147c545e))
* **worker:** name the surfaces a lane's process actually started on ([0433229](https://github.com/bkuberek/ropeshift/commit/04332297b4d6c281f39fd4efaaac99cef372e702))
* **worker:** note a failure that no run heard ([83889a3](https://github.com/bkuberek/ropeshift/commit/83889a3a3b08cabb38ed41ff30fb521a4eb9bb83))
* **worker:** order a prompt and a turn's end of one millisecond as they were heard ([48668bc](https://github.com/bkuberek/ropeshift/commit/48668bcafce76a12f738dfdb258bc97ba0cc4c98))
* **worker:** park a lane whose adoption fails while the sign-in is out ([9496a46](https://github.com/bkuberek/ropeshift/commit/9496a46d1006d203bca92fc19e4f0c09b1497458))
* **worker:** park an undeliverable blocking answer on every question the lane waits on ([d2927c0](https://github.com/bkuberek/ropeshift/commit/d2927c0ad581b9d51ad3c3641b2ec896605615cb))
* **worker:** park for an undeliverable answer only at rest, and park a restarted lane whose answer was settled ([6a1a2b3](https://github.com/bkuberek/ropeshift/commit/6a1a2b36100d4a08c531ebc38e8068c03ae35c34))
* **worker:** probe a version again when a check did not pass ([af5cdfa](https://github.com/bkuberek/ropeshift/commit/af5cdfa0e0ca27df5bb1af17f852b55fd7475ac0))
* **worker:** read a handoff a park left in the run under the flight's lock ([e92defe](https://github.com/bkuberek/ropeshift/commit/e92defee9cd6821e9c3d1a4a983c86cccd5b0ac6))
* **worker:** read a hook time past the clock as now ([ee97d64](https://github.com/bkuberek/ropeshift/commit/ee97d64857b2372421965118b2089796eba7e43a))
* **worker:** read a lane's profile again after a failed read ([2d7e490](https://github.com/bkuberek/ropeshift/commit/2d7e4904af43253ae765a0670994870caaa80d7d))
* **worker:** read a lane's repository profile layer at its settings base, never at a commit a lane wrote ([8aade0f](https://github.com/bkuberek/ropeshift/commit/8aade0f410805484d537c372b0e53c59ee2291ba))
* **worker:** read a prompt that lands while a turn's end is decided ([2bbe3ac](https://github.com/bkuberek/ropeshift/commit/2bbe3acbe87da0d960d4555bcada7693dfb85dfb))
* **worker:** read a replayed StopFailure as a past failure ([290a7cd](https://github.com/bkuberek/ropeshift/commit/290a7cd2d87493ec3441a8c2c53601fe61e371f3))
* **worker:** read a review lane's settings, servers, and status line at the commit the reviewed lane began from ([17a8f76](https://github.com/bkuberek/ropeshift/commit/17a8f76f90f38869cc427a08b868a4870e1fa947))
* **worker:** read a turn a typed line or background work started as working ([8b9c3c1](https://github.com/bkuberek/ropeshift/commit/8b9c3c117c3e8146f0699912cec7ad29166bdcb9))
* **worker:** read the first hook after a clock stepped back as newer than the time kept in its place ([319a0d1](https://github.com/bkuberek/ropeshift/commit/319a0d1a7f9ba5b5ec91320df01deeb6ad40633b))
* **worker:** read the journal again after a failed adoption read ([3338670](https://github.com/bkuberek/ropeshift/commit/3338670854d4e860e6c898a2ff0dad30cb4363d8))
* **worker:** read the profile of a review with no recorded base through one gates-only helper ([5d4f950](https://github.com/bkuberek/ropeshift/commit/5d4f950b94308ac53994be9ea2cf4c3d57e3bc32))
* **worker:** record a probe's attempt on the worker's own context ([366fa1d](https://github.com/bkuberek/ropeshift/commit/366fa1d8a2197853cc159dfa8d3c505c97922060))
* **worker:** record each probe before it starts and delay a restart's probe ([5e5ac7e](https://github.com/bkuberek/ropeshift/commit/5e5ac7e6b2e067167eccfb92f529f60d652f9688))
* **worker:** refuse a blocked report while the lane's own question is open ([b258685](https://github.com/bkuberek/ropeshift/commit/b2586856bb745a86dabe0110ba2ad834bd330831))
* **worker:** remove what a consultation's session left when it ends ([20d4d7e](https://github.com/bkuberek/ropeshift/commit/20d4d7e0d2a60998193e4958b4112de50e7e56f9))
* **worker:** report a profile that stays unreadable once until its words change ([ab68422](https://github.com/bkuberek/ropeshift/commit/ab6842243a04aa78bda0a33674c0f993c951190f))
* **worker:** report a refused claim of a woken lane ([7908191](https://github.com/bkuberek/ropeshift/commit/7908191302652264feb886a1b64229f6ef125cfb))
* **worker:** restart an orchestrator adopted in a failed start check ([e054e8d](https://github.com/bkuberek/ropeshift/commit/e054e8db92968553b8035461504532c860973aac))
* **worker:** resume a lane with an answer its session has no channel for, and report any other message undeliverable ([04d5bd2](https://github.com/bkuberek/ropeshift/commit/04d5bd27f248ecf69de076850c6b211a3f3bfa32))
* **worker:** run a contract review lane no less carefully than default ([dfbd8f5](https://github.com/bkuberek/ropeshift/commit/dfbd8f54d42a83a36564094f8e01b62073e1d665))
* **worker:** run review lanes and orchestrators no less carefully than default ([d6d68e7](https://github.com/bkuberek/ropeshift/commit/d6d68e75fa66df4ffcdc02281f80c0a566cbf35a))
* **worker:** settle an undeliverable message a crash left at the worker ([590b94e](https://github.com/bkuberek/ropeshift/commit/590b94e617c6887aa7776920c038c59018767478))
* **worker:** start a lane again that a stopping daemon cut off ([c7161b4](https://github.com/bkuberek/ropeshift/commit/c7161b49cc3d9e8426cd8fa19ecf9d2c485db559))
* **worker:** start an orchestrator again after a failed start ([93afc7c](https://github.com/bkuberek/ropeshift/commit/93afc7ce0e180c6e3ae6119268a685bb3ef270c8))
* **worker:** start no probe at a known usage limit ([df89ebf](https://github.com/bkuberek/ropeshift/commit/df89ebfa5cbe2d7ca8d106735ea7cf481def735e))
* **worker:** stop a consultation's session a clock-skew margin before its hold ends ([dbbfc41](https://github.com/bkuberek/ropeshift/commit/dbbfc41f4810d38b979eddb060e74e8d6eb8b332))
* **worker:** stop a consultation's session again when its stop read no end ([34e45cf](https://github.com/bkuberek/ropeshift/commit/34e45cf354d4c503d9b57582000a882c76a7de71))
* **worker:** stop a consultation's session before the hold on its question ends ([c83435d](https://github.com/bkuberek/ropeshift/commit/c83435db27ec45e3c644d9ccbf523a6fa4bb04c0))
* **worker:** take a consultation before its start reaches the journal ([296a6f5](https://github.com/bkuberek/ropeshift/commit/296a6f5132adeab55374ced76845d9c093174a75))
* **worker:** take only the gates of the layer a review with no recorded base reads ([2fd40a1](https://github.com/bkuberek/ropeshift/commit/2fd40a1f25d9017bde1f23d0ac50d10fc5d7cdaf))
* **worker:** tell an assistant's brief answer from a reply in every generation ([217ffdb](https://github.com/bkuberek/ropeshift/commit/217ffdb2df9155d8a7bc0e28b276d0b39590a25e))
* **worker:** tell an orchestrator's new run every item that stands ([76ee051](https://github.com/bkuberek/ropeshift/commit/76ee05136ad6e24e42c6c160a375aaccc6a1ac34))
* **worker:** treat an assistant's re-brief after a clear as no reply ([e39716a](https://github.com/bkuberek/ropeshift/commit/e39716a786f0d4c6a8fdeeb8c87ff3931bbfa39d))


### Reverts

* bound the wait on a refused nudge ([437c9d1](https://github.com/bkuberek/ropeshift/commit/437c9d159e72b82f388c00772c78c0cbf0d44675))
* date an old limit at the hook that found it ([dc9f378](https://github.com/bkuberek/ropeshift/commit/dc9f378e259d6d0f8863e2bf498b0b8897651274))
* **profiles:** restore default as the built-in permission mode ([efc01e4](https://github.com/bkuberek/ropeshift/commit/efc01e4be7dd2a5a526e0252e25a7383a2578896))
* **worker:** go back to one flag for a turn that left background work ([0ccc91e](https://github.com/bkuberek/ropeshift/commit/0ccc91efcefeadf6563da4b49c461257b04afa88))

## [0.1.0-alpha.16](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.15...v0.1.0-alpha.16) (2026-11-16)


### Features

* add a category from the web UI ([619f37d](https://github.com/bkuberek/ropeshift/commit/619f37dc987d046ea2b24a0c2d85a13b314cd9a6))
* add an agent from a pasted TOML table in the web UI ([8d54c02](https://github.com/bkuberek/ropeshift/commit/8d54c02604e04e57fcfe12730024377cfe7fc7cc))
* add raise_model_and_effort to the delegate policy ([c0cc047](https://github.com/bkuberek/ropeshift/commit/c0cc04762381538a1c104e8969dd9e6015791d65))
* aggregate finished work as a planning lane's delivery history ([e884ed0](https://github.com/bkuberek/ropeshift/commit/e884ed0967e9d81f25c9a056673758ea4fd086e5))
* answer a question where Open questions lists it ([c633e0f](https://github.com/bkuberek/ropeshift/commit/c633e0fb4e091022b040554546f7b7b176d898bf))
* claim and start ready work from the web UI ([ee6a3e6](https://github.com/bkuberek/ropeshift/commit/ee6a3e6cfa1e2b517aa86ecedb7c681341fb293f))
* compute estimate calibration from outcomes ([575a191](https://github.com/bkuberek/ropeshift/commit/575a191e6bb4140c26626c065696610559389dda))
* create a work order from the web UI ([aa473e7](https://github.com/bkuberek/ropeshift/commit/aa473e7413b1ccf31fa191e01a768eead3bf6ae3))
* edit the estimate policy from the web UI ([22ac88f](https://github.com/bkuberek/ropeshift/commit/22ac88f62083ddb7b88ddbd03926b5dabe1381ff))
* export the estimate calibration metrics ([93c1c10](https://github.com/bkuberek/ropeshift/commit/93c1c1093479899b903a119359fa93c603a9a49c))
* find a record to cite from the web UI ([5be55ad](https://github.com/bkuberek/ropeshift/commit/5be55ad620a4a584cd07eab01ec3c9125b4baf2a))
* have the assistant confirm before it raises a lane's model or effort ([e4ac34b](https://github.com/bkuberek/ropeshift/commit/e4ac34b848860d231596785a5114bdb0d6ffee1a))
* hold a delegate's raise to what the lane's roster agent runs ([c340d1c](https://github.com/bkuberek/ropeshift/commit/c340d1c63190dc19b8ac2c687292022a930272b8))
* hold a restricted delegate's lane from a worker that would drop its record ([70e90e4](https://github.com/bkuberek/ropeshift/commit/70e90e41075c6bb278b2b9854c4ce593fb42c1fa))
* let a planning lane read its delivery history with context_get ([0a73829](https://github.com/bkuberek/ropeshift/commit/0a738292ec5cfe87f4359d364ab322644c898212))
* name the repository in a merge request's detail ([5b2da8b](https://github.com/bkuberek/ropeshift/commit/5b2da8b525c47c5fc35382da6cf45d4b8c725708))
* overturn a decision from the web UI ([7b002e2](https://github.com/bkuberek/ropeshift/commit/7b002e2bf5f4fae0f28dcf091ec416a6c7fbe8c6))
* place lane worktrees outside every clone and refuse a root inside one ([79ac5b2](https://github.com/bkuberek/ropeshift/commit/79ac5b25c0b0dcc316ed8782e68e6a92ebad6d5e))
* put the delivery history in a planning lane's context pack ([a47d6d5](https://github.com/bkuberek/ropeshift/commit/a47d6d5653ef221215d1fec1f52c2d48b80bc13c))
* read estimate calibration with measures estimates ([e49256a](https://github.com/bkuberek/ropeshift/commit/e49256abd444a1bde944cd5cb91e20bfaf8752bf))
* read the delivery history a planning session sees ([5508238](https://github.com/bkuberek/ropeshift/commit/5508238cfe917e7f2e43ebfec58354640de24ee1))
* record a decision from the web UI ([04074d9](https://github.com/bkuberek/ropeshift/commit/04074d96826347a3d09cf2b8fd543b1626510e80))
* record and dismiss a question from the web UI ([ae66043](https://github.com/bkuberek/ropeshift/commit/ae660431361cdc85f95fde0492d9e98c9267dab1))
* record the delegate that sent a lane's own layer on its claim ([7772abf](https://github.com/bkuberek/ropeshift/commit/7772abf7d30c34b7d9024e72001d3c0f6353d9b0))
* record the estimates and forecasts a plan approval showed as its baseline ([b7301ea](https://github.com/bkuberek/ropeshift/commit/b7301ea07df9311967e25d8e246932dbd419c54a))
* recover work from a work order's page, the floor, and the palette ([adc6fa5](https://github.com/bkuberek/ropeshift/commit/adc6fa51356ff2f385e90c2dd9481edfcca2a2a1))
* refuse a delegate's raise of a lane's model or effort under a restricting policy ([96e19a2](https://github.com/bkuberek/ropeshift/commit/96e19a28881881e4728af1c7be708d208c55e72f))
* report a worktree root that would lie inside a registered clone ([25532d0](https://github.com/bkuberek/ropeshift/commit/25532d07a594747a72236b8a4568e57b517d2cfa))
* resolve a worktree directory for every installation ([165fbdc](https://github.com/bkuberek/ropeshift/commit/165fbdc2601e37b6ac15cf5a574c6421370c055c))
* retry failed work from the web UI, and say what each work order allows ([5e964ba](https://github.com/bkuberek/ropeshift/commit/5e964bab99239bc50cc1d063ad291a3334eb2a05))
* say a delegate policy refused a lane's start in its outcome ([327ff45](https://github.com/bkuberek/ropeshift/commit/327ff453e8e2e3064b31ab89ce60b76ec1fa3a1a))
* say why a work order's lane ended in work show ([f80c514](https://github.com/bkuberek/ropeshift/commit/f80c5141eef64e2601eb1e13bd656970cef784f5))
* serve measures delivery and plan forecast on the web listener ([6ed6ec6](https://github.com/bkuberek/ropeshift/commit/6ed6ec6c521d5fc737e27ac37126c28d4c350c38))
* serve notices, lane messages, lane cost, and each driver's doctor result to the web UI ([2b0eec0](https://github.com/bkuberek/ropeshift/commit/2b0eec06deb7227aa95bf7d29ce5b052dacfc841))
* serve work acceptance and work timeline on the web listener ([d4d3201](https://github.com/bkuberek/ropeshift/commit/d4d32011e4d29509fd17093de44c6bfcab1ef52d))
* set worker lane caps from the web UI ([236c573](https://github.com/bkuberek/ropeshift/commit/236c573d1fe02de572ab0cb2a6802e72946aa563))
* show a merge request as its own card ([f6c2406](https://github.com/bkuberek/ropeshift/commit/f6c2406ac931c27500bc660b257de2af97407c3d))
* show a plan's forecast on its page and on the Roadmap ([3293fbf](https://github.com/bkuberek/ropeshift/commit/3293fbf29234b6f438ba1be82f5789299df03ac7))
* show a project's Overview and its Reviews in the web UI ([5e93c87](https://github.com/bkuberek/ropeshift/commit/5e93c87381b39bc36b3662e8e07b1b2928ccdb7b))
* show a work order's changes and activity in the web UI ([fdbc728](https://github.com/bkuberek/ropeshift/commit/fdbc7281f55fff077c8b442481f6c4c3ce746d8e))
* show a work order's estimate beside its baseline in the web UI ([be06b1c](https://github.com/bkuberek/ropeshift/commit/be06b1c593b755b1b2a8c567f5309f824483ef40))
* show delivery measures in the Measures view ([23420c7](https://github.com/bkuberek/ropeshift/commit/23420c773cfcbc4788a8a0f98784a9eb4e5b8ccd))
* show estimate calibration in the Measures view's Estimates tab ([10524c9](https://github.com/bkuberek/ropeshift/commit/10524c9a352a33cd965095f302968a34d4fa6b70))
* show estimates and forecasts at plan approval and in the planning outline ([9af6c5c](https://github.com/bkuberek/ropeshift/commit/9af6c5c5db98bbaae042b35772da7f001815a56a))
* show notices, lane messages, lane cost, and each driver's doctor result in the web UI ([deb4d91](https://github.com/bkuberek/ropeshift/commit/deb4d91ba8b031dcf9921c6283180a68c4a36c33))
* show the delegate policy's raise setting in Settings ([e0f0308](https://github.com/bkuberek/ropeshift/commit/e0f0308623a2d463802fea7904c0838c0a8bfdd2))
* show the delivery history on the planning pane's History tab ([7dcd5e4](https://github.com/bkuberek/ropeshift/commit/7dcd5e491081de1ba175e37351ae24e1373ece6b))
* show what approving a plan registered of its manifest ([1e25c8f](https://github.com/bkuberek/ropeshift/commit/1e25c8fd282f5d8ee735506cdc66e6c7eec04341))
* start a contract review from the web UI ([a38266c](https://github.com/bkuberek/ropeshift/commit/a38266c7416ad5b7edfca7fb7c28b5dd1288c578))
* state each driver's doctor result in one domain function ([5ff3391](https://github.com/bkuberek/ropeshift/commit/5ff3391ecf17734178b31752eaf2b1175173e35b))
* tell a planning lane the estimates and forecasts of the version it submitted ([ba265ba](https://github.com/bkuberek/ropeshift/commit/ba265baac0120d1c5a0805e41646d97b517e6e1a))
* use the installation pool only from 3 other projects ([5fcced3](https://github.com/bkuberek/ropeshift/commit/5fcced34e00a8dee1b2004ae27533934cc18452f))
* **web:** focus the control an address names ([935934e](https://github.com/bkuberek/ropeshift/commit/935934e13379b55ac9378fefd38d1588405f3c21))
* **web:** go to Agents, Settings, and Projects with G then A, S, and P ([0f06e7e](https://github.com/bkuberek/ropeshift/commit/0f06e7eefdc7ef4d6a84c5008abb87a34aefed3c))
* **web:** list every operation that changes something in the command palette ([c5a4828](https://github.com/bkuberek/ropeshift/commit/c5a48288b97d6c528e950028d9eccb3781ac070f))
* **web:** name a milestone's forecast by its title on plan approval ([b9d61d8](https://github.com/bkuberek/ropeshift/commit/b9d61d84d8715b26acd8eb30bf0a729236a94b8c))
* write a planning lane's notice of a plan version's estimates and forecasts ([4fd203b](https://github.com/bkuberek/ropeshift/commit/4fd203bce7c0e163447923e26a0746cdfdca9cb7))
* write each baselined work order's and milestone's estimate outcome ([954a03c](https://github.com/bkuberek/ropeshift/commit/954a03cd7f4a367e73587a9da87d84f7a7922673))


### Bug Fixes

* **api:** stop serving the repository and policy reads no web control calls ([ac798fc](https://github.com/bkuberek/ropeshift/commit/ac798fc420cc46e82203d621cdef0188bbe2a95d))
* ask for HOST/OWNER/REPO in the forge binding field ([3e342d7](https://github.com/bkuberek/ropeshift/commit/3e342d75cb3b440cf18de9731924f5060c259ae2))
* **cli,web:** read a forecast that states no lower bound as having none ([bb52e1d](https://github.com/bkuberek/ropeshift/commit/bb52e1dc18010ee748bc302806d64abed5569b5f))
* compare a delegate's effort only where a session runs one ([35e25ef](https://github.com/bkuberek/ropeshift/commit/35e25eff0e6471bc5d0a7547a01377fb5ebca773))
* date a worker report no earlier than its session or the report before it ([8bc749f](https://github.com/bkuberek/ropeshift/commit/8bc749f093df497126ad00fc9a06b1f55ec344b5))
* hold a worker report's age to the window it stays observed ([5063063](https://github.com/bkuberek/ropeshift/commit/5063063f018fdca29845329609637bd81cd7f6dd))
* keep a lane's own words out of its failure detail ([9f118ed](https://github.com/bkuberek/ropeshift/commit/9f118edf5099c03f8fb57a41409a211b645f1aa9))
* keep only a lane's own worktree and check every clone the worker uses ([f80864e](https://github.com/bkuberek/ropeshift/commit/f80864e59f112fcf548b190fd02f7b1f5ccca3ff))
* keep pool record times out of a forecast's words and p85 ([9edf26e](https://github.com/bkuberek/ropeshift/commit/9edf26eeaf0e49cca328651e48fb1bc052335130))
* keep the phone top bar's buttons their size under a long project name ([79bdafa](https://github.com/bkuberek/ropeshift/commit/79bdafacedbda1a5eb1c2b7a51f2344be94f81e4))
* let a lane in flight keep its worktree when the default root moves ([4e5412f](https://github.com/bkuberek/ropeshift/commit/4e5412f62b6f678f66d53c8a70db6fa588172c86))
* offer a lease by protocol through the single-project link too ([b53e781](https://github.com/bkuberek/ropeshift/commit/b53e781df61fc1563825f944357513b924e04c9c))
* open a tier 6 band above its median when its p85 is another project's longest ([211b82e](https://github.com/bkuberek/ropeshift/commit/211b82e779109d2679e0190359a874a7f614582e))
* read a thin delivery history group at the estimator's ranks ([5c58b7d](https://github.com/bkuberek/ropeshift/commit/5c58b7ddce4b6bbcdb04581e185f4d04a876ae74))
* refuse a lane dispatch from a delegate whose ID names no one ([714fe0e](https://github.com/bkuberek/ropeshift/commit/714fe0e0cdb9158ac63c580aebcee8db224e365d))
* refuse a lane layer that changes the driver ([2e786c2](https://github.com/bkuberek/ropeshift/commit/2e786c2173375a17f58dd61ed79795872715e288))
* refuse a policy body that spells a key two ways ([bfb5ffc](https://github.com/bkuberek/ropeshift/commit/bfb5ffc0832b20f8ea8e245324c3b20a58960eb9))
* refuse two spellings of a key in a table inside a list ([6a00320](https://github.com/bkuberek/ropeshift/commit/6a003206f81ff53535387dfe01a2b434efe52a14))
* retry a fetch that loses the remote ref's lock ([f95ff48](https://github.com/bkuberek/ropeshift/commit/f95ff48620b708ac79c7d611c2da79eb5cebfc13))
* retry a review's and a merge's fetch that loses a ref's lock ([ce828f5](https://github.com/bkuberek/ropeshift/commit/ce828f5bf90034d9898105d1e1a03e38f1ecbdb4))
* retry the lane-ref fetch that loses the remote ref's lock ([1c41013](https://github.com/bkuberek/ropeshift/commit/1c410136635c04602147596dfb4a7bdd0140bd49))
* say the notice's estimates draw on finished work, as each explanation says ([c5ea0cb](https://github.com/bkuberek/ropeshift/commit/c5ea0cb0ad409df2e260cd28221bac128d227c60))
* send each planning lane its notice though another one failed ([09105b9](https://github.com/bkuberek/ropeshift/commit/09105b983d1c3c7cbaa0725dc663f548f107579d))
* serialize worktree changes within one repository ([76e6c59](https://github.com/bkuberek/ropeshift/commit/76e6c590f6df72ae342c9aa4a84646f7a572418c))
* show a claim held until its worker upgrades in the stuck view and Needs you ([f2936f3](https://github.com/bkuberek/ropeshift/commit/f2936f384777d14f8a1361657d8aa8163a4c394f))
* **web:** clear a toast's timer when it is dismissed or its provider unmounts ([27df3e7](https://github.com/bkuberek/ropeshift/commit/27df3e771763c8eb19caef97c377d7752fd068b4))

## [0.1.0-alpha.15](https://github.com/bkuberek/ropeshift/compare/v0.1.0-alpha.14...v0.1.0-alpha.15) (2026-11-15)

### Notes

* **Security:** this release closes a hole in lane overrides. Before it, a delegate credential could start a lane whose overrides ran a command on the worker, as the worker's own user. Upgrade every machine that runs ropeshift, remote workers included, because the worker enforces the rule. Review any outside delegate credential issued before you upgrade, and revoke the ones you do not need with `ropeshift delegate revoke <id>`. The release also stops a delegate from recording a decision under the ID a plan's draft derives, which let its answer stand in place of the one a member approved.
* This release adds database migrations `0086` (estimate sets and each work order's current estimate) and `0097` (the delegate that proposed each event), and the installation database's migration `0024` (the installation's delivery pool). They run on the first start. Back up each project with `ropeshift backup <file>` and the installation with `ropeshift backup <file> --installation` before you upgrade.
* **Behavior change:** a lane's overrides, from `lane start --profile` or `overrides` in the lane start API, cannot set `hooks`, `agent.credentials`, `agent.instructions_file`, `worktree.root`, or `worktree.branch_prefix`. They cannot raise `max_generations_per_day`, `max_restarts`, `review.max_review_rounds`, or `resources.ports_per_lane`, or shorten `restart_window`. The API answers 400 `invalid`, for every caller and in any spelling of the key, to overrides that set the first five, that set `review.max_review_rounds` to anything but `1` or `resources.ports_per_lane` to anything but `0`, or that spell one key two ways. The worker refuses the rest when it reads the layer. A lane whose stored overrides break these rules, one requested before the upgrade included, fails to start. Overrides of other keys, such as `agent.model` and `agent.effort`, work as before.
* **Behavior change:** the `agent.gates` of every profile layer join: yours, the repository's `.ropeshift/profile.toml`, and the lane's, in that order and with each gate once. A layer adds gates and never removes one, and `gates = []` removes nothing. Any profile layer that spells one key two ways, such as `[hooks]` and `[Hooks]`, is refused, naming both spellings. Before you upgrade, check profiles that relied on a repository or lane layer replacing your gates, profiles that spell a key two ways, and scripts that pass hooks, credentials, or worktree settings through `lane start --profile`.
* **Behavior change:** a delegate policy can exempt only four actions: accepting verified work (`work_order.accept`), rework after a failed verdict (`work_order.rework`), creating a work order (`work_order.create`), and starting a contract review (`plan.start_review`). A new version that lists any other operation is refused. A stored version that lists one exempts nothing for that entry, and the delegate policy form in Settings drops it.
* **Behavior change:** a delegate's accept counts as verified only once the review of the work's current attempt has ruled, every criterion passed on a check the lane did not write, no verdict for the attempt is `fail` or `cannot_judge`, and every check the base branch requires is green on the work's pull request. Otherwise it becomes a confirmation card. Recording a decision is a card for every delegate, the assistant included, even when the decision policy covers it. An outside delegate's message to a lane is a card; the assistant's still sends at once. Passing a review, planning work's included, is a card for every delegate, and a delegate's acceptance of a contract review passes none of the work it read. Every event a confirmed card records names the member as its actor and the delegate as its proposer.
* **Behavior change:** decision IDs that start with `pd-` are reserved for plan approval, and recording, answering, or overturning with one answers 400 `invalid` for any client, a member's included. A plan approval answers 409 `decision_clash`, and `plan approve` exits 4, while a decision held under an ID one of the version's drafts derives says something else or was not recorded by a plan approval. The refusal names each clashing decision, who recorded it, and its draft, and approves nothing. To clear a clash, overturn the decision if it should not stand and approve a new version of the plan.
* A member's approval of a plan version registers each repository that version's manifest lists and the project lacks, from the clone at its path beside the planning repository's clone on the daemon's worker. A path must lead, with every symbolic link followed, inside the directory that holds the planning clone, and a repository a member removed is not registered again. `ropeshift repository add --manifest <file>` registers what a manifest lists with no approval, and only a member can run it. `plan show`, `plan approve`, and the web UI's plan approval page show what approving registers. Work in a repository whose worker keeps no clone of it now waits as `no_checkout`, and a milestone across repositories at `person_merges` with review target `branch` reads "accepted, not merged".
* `ropeshift work timeline <id>` prints where a work order's time went, and `ropeshift measures delivery` reads look back over delivery records, grouped by kind, size, agent, repository, or project. The daemon exports the delivery measures as `ropeshift.measure.delivery.*` metrics.
* Ropeshift estimates each open `change` and `rework` work order's delivery time and cost from comparable finished work, at intake and when work is made, and estimates again every `--delivery-tick` as history grows. `ropeshift work show` prints the current estimate with its explanation. A member sets how estimates read history with a versioned `[estimate_policy]`; its `share_history`, off by default, adds the project's records to the installation's pool and draws on the pool. Ropeshift also forecasts a plan's milestones with a seeded simulation, which no command shows yet.


### Features

* estimate each plan version's work at intake and work made outside a plan ([55b3453](https://github.com/bkuberek/ropeshift/commit/55b345364c3307438b3eb3aaa7c3903c3478b197))
* estimate new work from comparable finished work ([2533870](https://github.com/bkuberek/ropeshift/commit/25338705f2979596fc1b353a1410dc05f522b666))
* export the delivery measures and read look back by project ([6c83d0c](https://github.com/bkuberek/ropeshift/commit/6c83d0c67285587d9567872c1b38005b3191a0ea))
* forecast a plan's milestones with a seeded simulation ([db215b3](https://github.com/bkuberek/ropeshift/commit/db215b389885f031593f595e8975ada6f6ff35c8))
* give an estimate the fields of its data model ([38f4c61](https://github.com/bkuberek/ropeshift/commit/38f4c612269ede8873cc2344d3aaa182f2be0f75))
* keep each open work order's current estimate as history grows ([2a1dcc4](https://github.com/bkuberek/ropeshift/commit/2a1dcc4e6ef4c14927decc616c81e3225efac338))
* keep estimate sets and each work order's current estimate ([318f7ac](https://github.com/bkuberek/ropeshift/commit/318f7acb2630bff83f2a51e05be8ab4b71470861))
* name what caused a registration from a manifest in its event ([b3fa7d7](https://github.com/bkuberek/ropeshift/commit/b3fa7d7e80ec42801e2b3b53db77eb49128b4c60))
* publish the estimate policy as a member's version ([a98c41a](https://github.com/bkuberek/ropeshift/commit/a98c41a2a293c797139190436445c8fb5e23297c))
* register what an approved manifest adds, and from a manifest by hand ([48c7329](https://github.com/bkuberek/ropeshift/commit/48c732989fd5f56cdeed538ca3ccc1c3202c77e5))
* share delivery history through the installation pool ([f6a98ca](https://github.com/bkuberek/ropeshift/commit/f6a98ca4ae2699381914fc7e15c968f23d638797))
* show a milestone across repositories as accepted, not merged ([3f91cb7](https://github.com/bkuberek/ropeshift/commit/3f91cb7a8f01f3d1341884878ccef05acb786b9d))
* show a work order's current estimate with its explanation ([3bd667d](https://github.com/bkuberek/ropeshift/commit/3bd667d77b765491a49636ad796a3e3bf8b3a6c6))
* show on the plan approval page what approving registers ([de7a875](https://github.com/bkuberek/ropeshift/commit/de7a8751f87d2b5dede0d4588ac99ff1cca1ae6b))
* show what approving a plan registers, before and after ([0a4d4fa](https://github.com/bkuberek/ropeshift/commit/0a4d4fa3b31001034d3f8289f737648a3f162104))


### Bug Fixes

* accept the floor values of a lane's overrides ([99eed18](https://github.com/bkuberek/ropeshift/commit/99eed1850b78033ca9489631eb30fedebe2d4e62))
* compare manifest paths with the planning clone and reserved directories by file ([3af85ef](https://github.com/bkuberek/ropeshift/commit/3af85ef972e17a24b51a7e287c4df6c33b6ab3c3))
* count a pd- decision as plan approval's own only when a member recorded it ([3c9bdec](https://github.com/bkuberek/ropeshift/commit/3c9bdec2a3e81688babba23a18030022f30d5594))
* draw the same estimate whatever order its records come in ([ba73d38](https://github.com/bkuberek/ropeshift/commit/ba73d38790a620a133025e4ab4df32cd3d462bf5))
* drop exemptions no policy may hold when the delegate policy form loads ([bd27334](https://github.com/bkuberek/ropeshift/commit/bd27334ec53354f84b710952205aa65812ad88ba))
* drop stale exemptions from the delegate policy form's saved value ([23344cb](https://github.com/bkuberek/ropeshift/commit/23344cb53cd2569b585509330459e58886c5da7b))
* estimate safely under any policy and read a size left out as unknown ([4d22a7e](https://github.com/bkuberek/ropeshift/commit/4d22a7e7cdcbb5540ef33398b98ac4b5cbdf8100))
* hold work as no_checkout when the kept checkout's directory is gone ([068fbe9](https://github.com/bkuberek/ropeshift/commit/068fbe979a2f04ba4da8829eb720477b87a31665))
* hold work as no_checkout where the worker keeps no clone of its repository ([94085da](https://github.com/bkuberek/ropeshift/commit/94085dab8a89c381f238730723884684db498305))
* keep a delegate's accept a card while the review is open ([68a792c](https://github.com/bkuberek/ropeshift/commit/68a792cd1c93db772085e72ae487d14a39ac6e26))
* keep a removed repository out of manifest registration by remote ([7199ae4](https://github.com/bkuberek/ropeshift/commit/7199ae4efb9c8cd631213006789b6996690418fe))
* keep an archived project's share fence and answer a pending take-out truthfully ([388ba2f](https://github.com/bkuberek/ropeshift/commit/388ba2f7209e0788f9654a85b3a8628010fc6ad3))
* keep every gate below a lane's own profile layer ([0bf39fb](https://github.com/bkuberek/ropeshift/commit/0bf39fb61fc83504a4cb14f506fd7acf44263bfe))
* keep manifest paths inside the directory that holds the planning clone ([d8b8376](https://github.com/bkuberek/ropeshift/commit/d8b83761e5461c26856475f1b3f6485051f357f1))
* keep only done work in the pool and take a share out whenever a project stops sharing ([f4c6dde](https://github.com/bkuberek/ropeshift/commit/f4c6dde8dd0d1ad3b2ff05b165c441eb062946fa))
* label each forecast from all the work that competes for its lanes ([bcdbc15](https://github.com/bkuberek/ropeshift/commit/bcdbc15f63f670075f4355f3b950c6ed41f59be1))
* let a delegate policy exempt only reversible acts ([d293f7f](https://github.com/bkuberek/ropeshift/commit/d293f7f519e42e65dd73a5ba4f0714fb91cb60b0))
* make a delegate's decision within policy a consent action ([fe184b4](https://github.com/bkuberek/ropeshift/commit/fe184b4db43cbe37806267a3811c8bc41e292f29))
* make an outside delegate's message to a lane a card ([b699539](https://github.com/bkuberek/ropeshift/commit/b69953995242e72747f5c1e41157dbe4d466fbc5))
* make passing a plan review a card for every delegate ([283b611](https://github.com/bkuberek/ropeshift/commit/283b61174ab066001029b922a470bb76c03f8615))
* make registering from a manifest a member's alone ([5f8e4a3](https://github.com/bkuberek/ropeshift/commit/5f8e4a3d378bb11f5d6461da9d77af3a3f8fe5ac))
* merge gates from every profile layer ([f74dd09](https://github.com/bkuberek/ropeshift/commit/f74dd091fff488346fc36c50d83b1f1053ca8944))
* name the delegate that prepared a card as the proposer of what it made ([c06f9cd](https://github.com/bkuberek/ropeshift/commit/c06f9cd2f13675031b229322beb95d32ea8eeccd))
* name the planning repository when a manifest adds a second one ([3d6a028](https://github.com/bkuberek/ropeshift/commit/3d6a0285249640715b99289bdd692e144fc69f91))
* read a lane cap of zero as no cap in the forecast, as the product does ([9ae07d6](https://github.com/bkuberek/ropeshift/commit/9ae07d659f2aeb48913d314329845374d1227b02))
* read plan approval's own decision through the authorization package ([27503c5](https://github.com/bkuberek/ropeshift/commit/27503c5a632e77d370e6f277d6b41719d304acdb))
* record the manifest entries a stopped registration never attempted ([20601f5](https://github.com/bkuberek/ropeshift/commit/20601f5406ffda163f0e89c1cea48b2d4924010c))
* refuse a delegate's accept when a review verdict failed or could not judge ([ffc0b52](https://github.com/bkuberek/ropeshift/commit/ffc0b52ab3c08778c0e487cb0412eab6e81aa6a5))
* refuse a lane layer that changes agent.instructions_file ([c58c383](https://github.com/bkuberek/ropeshift/commit/c58c3836c535173309b624554f03f2bf9400b7fa))
* refuse a lane layer that raises max_review_rounds ([2dd9dc6](https://github.com/bkuberek/ropeshift/commit/2dd9dc6a10631e53eee3214919e6f125c34789c6))
* refuse a lane layer that raises ports_per_lane ([558350b](https://github.com/bkuberek/ropeshift/commit/558350b764e877d998e948a5d26c7d59c384054f))
* refuse a plan approval whose draft would decide over another decision ([5eabb8b](https://github.com/bkuberek/ropeshift/commit/5eabb8b7c932015df3a9c91f5bd7ff6ed4f5a5cb))
* refuse lane overrides in every spelling the profile reads ([2aef232](https://github.com/bkuberek/ropeshift/commit/2aef2326f5193ce610905ef3406b1cc0f6b97ff2))
* refuse lane overrides that carry hooks, credentials, paths, or higher caps ([fec4833](https://github.com/bkuberek/ropeshift/commit/fec48335c6ca177651e5a40da2006cb064133ebc))
* refuse two spellings of one profile key ([f452060](https://github.com/bkuberek/ropeshift/commit/f45206078245cc11dad11d7b1065b0365b2ae994))
* register a plan approval's manifest once ([f2f104b](https://github.com/bkuberek/ropeshift/commit/f2f104b6eff113778d31332d49c52cedc2c101fd))
* register only what the approved version's manifest lists ([94624a0](https://github.com/bkuberek/ropeshift/commit/94624a0c93dafa5fc50d95aca7339be96f0b2076))
* report a manifest name another repository holds as a collision ([988cd88](https://github.com/bkuberek/ropeshift/commit/988cd88c0ef3a811fe49d9d9e8c92f64edd8ed71))
* reserve plan approval's derived decision IDs and name every clashing draft ([b762c2e](https://github.com/bkuberek/ropeshift/commit/b762c2e3f595168f2443b6b15c5ad835bd949962))
* say durations apart and carry them across units, and one outcome as one ([930ef45](https://github.com/bkuberek/ropeshift/commit/930ef450d88def20f8d0fe5ff60a8b769713b9b7))
* show what a plan approval registers before the member approves ([753cdbd](https://github.com/bkuberek/ropeshift/commit/753cdbd15eff8f023f3298ffaaffa1ae8a9f8645))

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
