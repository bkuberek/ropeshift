import { useCallback, useState } from "react";

import { useRead } from "../admin/parts";
import { readView } from "../api/client";
import type { ShopFloor } from "../api/types";
import type { Live } from "../api/useView";
import { readConversationEntries, readConversations, readConversationTargets, readLaneStream, readOrchestratorStream } from "../api/talk";
import type { ConversationList, ConversationTargets, TalkEntry, TalkPage } from "../api/talk";
import { mergePages } from "./entries";
import type { Target } from "./target";
import { targetKey } from "./target";

// What the surface reads. Every read follows the ledger's stream, so a change anywhere, a card decided in
// Needs you or a reply the assistant wrote, shows here without a reload, and none of it starts a session.

// useTargets reads what the member can talk to now, and the assistant policy's idle stops.
export function useTargets(live: Live, project: string) {
  return useRead<ConversationTargets>(() => readConversationTargets(), `talk:targets:${project}`, live);
}

// useConversationList reads the member's conversations with the assistant, newest activity first, and the workers
// that could run one. A list nobody asked for is not read.
export function useConversationList(live: Live, project: string, wanted: boolean) {
  return useRead<ConversationList | null>(() => (wanted ? readConversations() : Promise.resolve(null)), `talk:conversations:${project}:${wanted}`, live);
}

// useLaneEnd says why a lane that left the list of targets ended, from the floor, which still lists it.
export function useLaneEnd(live: Live, project: string, lane: string): string {
  const read = useRead<ShopFloor | null>(() => (lane ? readView<ShopFloor>("shop-floor") : Promise.resolve(null)), `talk:floor:${project}:${lane}`, live);
  const found = read.view?.lanes.find((row) => row.id === lane);
  const last = found?.generations?.[found.generations.length - 1];
  return last?.end_reason ?? "";
}

type Older = { key: string; entries: TalkEntry[]; next: string | null };

export type Stream = {
  entries: TalkEntry[];
  // ready says the target's own newest page has been read, so a stream never shows the last target's entries.
  ready: boolean;
  failure: string;
  refusedCode: string;
  asOf: number;
  readAt: number;
  // more says an older page exists, and loadOlder reads it.
  more: boolean;
  loadingOlder: boolean;
  loadOlder: () => Promise<void>;
  again: () => void;
};

type Newest = { key: string; page: TalkPage };

function fetchPage(target: Target, conversation: string, before: string): Promise<TalkPage> {
  switch (target.kind) {
    case "assistant":
      return conversation ? readConversationEntries(conversation, before) : Promise.resolve({ entries: [], as_of: 0 });
    case "orchestrator":
      return readOrchestratorStream(target.id, before);
    case "planning":
    case "lane":
      return readLaneStream(target.id, before);
  }
}

// useStream reads a target's stream: its newest page, which follows the ledger, and older pages a member asks
// for, which are kept until the target changes. An entry a newer read holds takes its newer form.
export function useStream(target: Target, conversation: string, live: Live): Stream {
  const key = `${targetKey(target)}\u0000${conversation}`;
  const read = useRead<Newest>(() => fetchPage(target, conversation, "").then((page) => ({ key, page })), `talk:stream:${key}`, live);
  const [older, setOlder] = useState<Older>({ key, entries: [], next: null });
  const [loadingOlder, setLoadingOlder] = useState(false);
  const here: Older = older.key === key ? older : { key, entries: [], next: null };
  const newest = read.view && read.view.key === key ? read.view.page : null;
  const cursor = here.next === null ? (newest?.next ?? "") : here.next;
  const loadOlder = useCallback(async () => {
    if (!cursor || loadingOlder) {
      return;
    }
    setLoadingOlder(true);
    try {
      const page = await fetchPage(target, conversation, cursor);
      setOlder((was) => {
        const base = was.key === key ? was : { key, entries: [], next: null };
        return { key, entries: mergePages(page.entries, base.entries), next: page.next ?? "" };
      });
    } finally {
      setLoadingOlder(false);
    }
  }, [cursor, loadingOlder, target.project, target.kind, target.id, conversation, key]);
  return {
    entries: newest ? mergePages(here.entries, newest.entries) : [],
    ready: newest !== null,
    failure: read.failure,
    refusedCode: read.refusedCode,
    asOf: newest?.as_of ?? 0,
    readAt: read.readAt,
    more: cursor !== "",
    loadingOlder,
    loadOlder,
    again: read.again,
  };
}
