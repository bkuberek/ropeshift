import type { WorkNeed } from "../api/types";
import { addressOf, Link } from "../app/route";

// setupHelp is the guide that says where each piece of a member's own agent CLI setup is set up, and how to keep it out
// of lanes or give a project's lanes what they need.
export const setupHelp = "https://github.com/bkuberek/ropeshift/blob/main/docs/settings-and-agents.md#your-own-agent-cli-setup";

// The choices a member has about a lane that loaded their own setup, as the recommendation names them.
const choiceWords: Record<string, string> = {
  ignore: "ignore it this time and open the setting",
};

// PersonalSetupChoices is what a member can do about a lane whose session loaded their own agent CLI setup despite the
// flags that keep it out, and which of it the card recommends. Ignoring it is leaving the lane running, so the card has
// no button and goes when the session that loaded the setup ends. The one way to stop a running lane is to cancel its
// work, which a leak does not warrant, so the card names it on the work order's page and does not offer it.
export function PersonalSetupChoices({ work }: { work: WorkNeed }) {
  const recommended = work.recommendation ? choiceWords[work.recommendation] : undefined;
  const order = work.work_order?.id;
  return (
    <>
      <ul className="criteria" aria-label="Choices">
        <li>
          <strong>Ignore it this time</strong>: leave the lane running. This card goes when its session ends.
        </li>
        <li>
          <a href={setupHelp} target="_blank" rel="noreferrer">
            Open the setting
          </a>
          : each piece above says where it is set up. Remove it there so the next lanes do not load it, or put what the project's lanes need in the
          repository.
        </li>
        <li>
          <strong>Stop the lane</strong>: only Cancel this work stops a running lane, and it ends the work for good.
          {order ? (
            <>
              {" "}
              <Link to={addressOf("/work-order", { id: order })}>Cancel this work on work order {order}</Link>.
            </>
          ) : null}
        </li>
      </ul>
      {recommended ? (
        <p>
          Recommended: {recommended}
          {work.recommendation_rationale ? `, because ${work.recommendation_rationale}` : ""}.
        </p>
      ) : null}
    </>
  );
}
