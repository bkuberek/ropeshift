import { addressOf, Link } from "../app/route";
import { Icon } from "../ui/Icon";

// OpenRunWindow leads a member to the run window of a machine whose orchestrator waits for one, where Open window opens
// it for the hours they choose. Only a member opens a window, and the server refuses anyone else there.
export function OpenRunWindow({ worker, name }: { worker: string; name: string }) {
  return (
    <Link className="btn btn-sm" to={addressOf("/settings", { section: "autonomy", worker })} installation aria-label={`Open run window on ${name}`}>
      <Icon name="clock" size="sm" />
      Open run window
    </Link>
  );
}
