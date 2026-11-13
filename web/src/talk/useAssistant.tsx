import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";

import { newID } from "../admin/logic";
import {
  clearConversation,
  compactConversation,
  createConversation,
  deleteConversation,
  openConversation,
  renameConversation,
  sendToAssistant,
  stopConversation,
} from "../api/talk";
import type { AssistantWorker, Conversation } from "../api/talk";
import { Button } from "../ui/controls";
import { Refusal } from "../ui/display";
import { Field } from "../ui/forms";
import { Dialog, useToast } from "../ui/overlays";
import { useTalk } from "./state";

// What a member does with the assistant's conversations: start one, resume one, and stop, compact, clear,
// rename, and delete the one they are in. A session starts on the member's first keystroke in the composer, on
// choosing a conversation, and on Resume, and on nothing else: opening the dock, loading a page, and reading a
// conversation's history start none, and closing the dock stops none.

// whichWorker says where a session starts: the worker the conversation last ran on while it can, the only
// worker that can, or one the member must choose among several. With none, the API refuses and says why.
export function whichWorker(conversation: Conversation | null, workers: AssistantWorker[]): { worker: string } | { choose: AssistantWorker[] } {
  const available = workers.filter((worker) => worker.available);
  if (conversation?.worker_id && available.some((worker) => worker.id === conversation.worker_id)) {
    return { worker: "" };
  }
  return available.length > 1 ? { choose: available } : { worker: "" };
}

type Purpose = "warm" | "resume" | "new" | "send";

type Choosing = { purpose: Purpose; body: string; workers: AssistantWorker[] };

export type Assistant = {
  // warm is the member's first keystroke: it starts a stopped session so it is up when the message goes.
  warm: () => void;
  send: (body: string) => Promise<boolean>;
  newConversation: () => void;
  resume: () => void;
  stop: () => void;
  compact: () => void;
  clear: () => void;
  askRename: () => void;
  askDelete: () => void;
  busy: boolean;
  failure: string;
  dialogs: ReactNode;
};

type Options = {
  conversation: Conversation | null;
  workers: AssistantWorker[];
  // refresh reads the conversation, its stream, and the targets again after a change.
  refresh: () => void;
};

const warmGapMs = 5000;

// stoppedNote says the running conversation a start stopped first, which a member should be told.
const stoppedNote = "The running conversation stopped first.";

export function useAssistant({ conversation, workers, refresh }: Options): Assistant {
  const talk = useTalk();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState("");
  const [choosing, setChoosing] = useState<Choosing | null>(null);
  const [renaming, setRenaming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const running = useRef(false);
  // A conversation a keystroke or a send makes has one ID, so a retry makes it once.
  const made = useRef("");
  const warmed = useRef(0);

  const stateOf = conversation?.actual_state ?? "stopped";
  // A key starts a session only from a stop. One that could not start is started again by Resume, so typing
  // does not ask for it again and again.
  const asleep = !conversation || stateOf === "stopped";

  const act = async <T,>(what: () => Promise<T>): Promise<T | undefined> => {
    if (running.current) {
      return undefined;
    }
    running.current = true;
    setBusy(true);
    setFailure("");
    try {
      const done = await what();
      refresh();
      return done;
    } catch (error) {
      setFailure(error instanceof Error ? error.message : String(error));
      return undefined;
    } finally {
      running.current = false;
      setBusy(false);
    }
  };

  // The conversation a start runs in: the one the member is in, or a new one with an ID of its own.
  const start = async (worker: string, create: boolean): Promise<string | undefined> => {
    const note = (stopped: Conversation[]) => stopped.length > 0 && toast("neutral", stoppedNote);
    if (!create && conversation) {
      const opened = await openConversation(conversation.id, worker);
      note(opened.stopped);
      return conversation.id;
    }
    made.current = made.current || newID();
    const created = await createConversation(made.current, "", worker);
    note(created.stopped);
    talk.select("assistant", created.conversation.id, { quiet: true });
    made.current = "";
    return created.conversation.id;
  };

  const sendTo = async (body: string, worker: string): Promise<boolean> => {
    const sent = await act(async () => {
      const id = conversation ? conversation.id : await start(worker, true);
      if (!id) {
        return false;
      }
      const reply = await sendToAssistant(id, body, conversation ? worker : "");
      if (reply.stopped.length > 0) {
        toast("neutral", stoppedNote);
      }
      talk.select("assistant", id, { quiet: true });
      return true;
    });
    return sent === true;
  };

  // begin starts a session where the rules say, or asks which worker when several could. It says whether the
  // act was done, so a message that waits on the member's choice is not cleared from the composer.
  const begin = async (purpose: Purpose, body = ""): Promise<boolean> => {
    const where = whichWorker(purpose === "new" ? null : conversation, workers);
    if ("choose" in where) {
      setChoosing({ purpose, body, workers: where.choose });
      return false;
    }
    return run(purpose, body, where.worker);
  };

  const run = async (purpose: Purpose, body: string, worker: string): Promise<boolean> => {
    if (purpose === "send") {
      return sendTo(body, worker);
    }
    const done = await act(() => start(worker, purpose === "new"));
    return done !== undefined;
  };

  const assistant: Assistant = {
    warm: () => {
      // A start asked a moment ago is still on its way, so the next key asks nothing.
      if (asleep && !running.current && !choosing && Date.now() - warmed.current > warmGapMs) {
        warmed.current = Date.now();
        void begin("warm");
      }
    },
    send: (body) => begin("send", body),
    newConversation: () => void begin("new"),
    resume: () => void begin("resume"),
    stop: () => {
      if (conversation) {
        void act(() => stopConversation(conversation.id));
      }
    },
    compact: () => {
      if (conversation) {
        void act(async () => {
          await compactConversation(conversation.id);
          toast("success", "Asked the session to compact its context.");
        });
      }
    },
    clear: () => {
      if (conversation) {
        void act(async () => {
          await clearConversation(conversation.id);
          toast("success", "Cleared: a new generation starts with an empty context. Earlier messages stay readable.");
        });
      }
    },
    askRename: () => setRenaming(true),
    askDelete: () => setDeleting(true),
    busy,
    failure,
    dialogs: null,
  };

  assistant.dialogs = (
    <>
      <WorkerDialog
        choosing={choosing}
        busy={busy}
        onClose={() => setChoosing(null)}
        onChoose={async (worker) => {
          const at = choosing;
          if (!at) {
            return;
          }
          setChoosing(null);
          if (await run(at.purpose, at.body, worker)) {
            if (at.purpose === "send") {
              talk.setDraft(talk.target, "");
            }
          }
        }}
      />
      <RenameDialog
        open={renaming}
        current={conversation?.name ?? ""}
        onClose={() => setRenaming(false)}
        onRename={async (name) => {
          if (!conversation) {
            return "No conversation to rename.";
          }
          try {
            await renameConversation(conversation.id, name);
            refresh();
            setRenaming(false);
            return "";
          } catch (error) {
            return error instanceof Error ? error.message : String(error);
          }
        }}
      />
      <DeleteDialog
        open={deleting}
        name={conversation?.name ?? ""}
        onClose={() => setDeleting(false)}
        onDelete={async () => {
          if (!conversation) {
            return "No conversation to delete.";
          }
          try {
            await deleteConversation(conversation.id);
            talk.select("assistant", "", { quiet: true });
            refresh();
            setDeleting(false);
            toast("success", `Deleted “${conversation.name}”. Its actions and cards stay in the audit.`);
            return "";
          } catch (error) {
            return error instanceof Error ? error.message : String(error);
          }
        }}
      />
    </>
  );
  return assistant;
}

const purposeWords: Record<Purpose, string> = {
  warm: "Choose where the assistant's session starts.",
  resume: "Choose where the session resumes.",
  new: "Choose where the new conversation's session starts.",
  send: "Choose where the session starts. Your message is sent once it runs.",
};

// WorkerDialog asks which worker runs the session, when several can.
function WorkerDialog({ choosing, busy, onClose, onChoose }: { choosing: Choosing | null; busy: boolean; onClose: () => void; onChoose: (worker: string) => void }) {
  const [picked, setPicked] = useState("");
  const names = choosing?.workers.map((worker) => worker.name).join(" and ") ?? "";
  const chosen = picked || choosing?.workers[0]?.id || "";
  return (
    <Dialog
      open={choosing !== null}
      title="Choose a worker"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={busy} disabled={busy || !chosen} onClick={() => onChoose(chosen)}>
            Start the session
          </Button>
        </>
      }
    >
      <p>{choosing ? purposeWords[choosing.purpose] : ""}</p>
      <fieldset className="stack">
        <legend className="visually-hidden">{`Workers that can run the assistant: ${names}`}</legend>
        {choosing?.workers.map((worker) => (
          <label key={worker.id} className="option">
            <input type="radio" name="assistant-worker" value={worker.id} checked={chosen === worker.id} onChange={() => setPicked(worker.id)} />
            <span className="option-text">{worker.name}</span>
          </label>
        ))}
      </fieldset>
    </Dialog>
  );
}

function RenameDialog({ open, current, onClose, onRename }: { open: boolean; current: string; onClose: () => void; onRename: (name: string) => Promise<string> }) {
  const [name, setName] = useState(current);
  const [failure, setFailure] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) {
      setName(current);
      setFailure("");
    }
  }, [open, current]);
  const valid = name.trim().length >= 1 && name.trim().length <= 80;
  const save = async () => {
    setBusy(true);
    setFailure(await onRename(name.trim()));
    setBusy(false);
  };
  return (
    <Dialog
      open={open}
      title="Rename conversation"
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={busy} disabled={busy || !valid} onClick={() => void save()}>
            Rename
          </Button>
        </>
      }
    >
      <form
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          if (valid && !busy) {
            void save();
          }
        }}
      >
        <Field label="Name" help="1 to 80 characters." error={name.trim().length > 80 ? "A name is at most 80 characters." : undefined}>
          <input className="input" autoComplete="off" value={name} onChange={(event) => setName(event.target.value)} />
        </Field>
        {failure ? <Refusal>{failure}</Refusal> : null}
      </form>
    </Dialog>
  );
}

function DeleteDialog({ open, name, onClose, onDelete }: { open: boolean; name: string; onClose: () => void; onDelete: () => Promise<string> }) {
  const [failure, setFailure] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) {
      setFailure("");
    }
  }, [open]);
  return (
    <Dialog
      open={open}
      title={`Delete “${name}”`}
      onClose={onClose}
      footer={
        <>
          <span className="spacer" />
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="danger"
            loading={busy}
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setFailure(await onDelete());
              setBusy(false);
            }}
          >
            Delete conversation
          </Button>
        </>
      }
    >
      <p>
        Its session stops, and its messages are deleted. What the assistant did stays: its actions are in the audit, and the cards it prepared stay in Needs you
        and in the audit. This cannot be undone.
      </p>
      {failure ? <Refusal>{failure}</Refusal> : null}
    </Dialog>
  );
}
