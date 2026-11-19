import { addressOf, Link } from "../app/route";
import { words } from "../parts/parts";
import { AddCategory } from "./AddCategory";

// CategoryFilter narrows a list of records to one category, a link each so the address keeps the filter, and
// adds a category beside them. path is the view's own address, and keep the other filters its links keep.
export function CategoryFilter({ path, categories, category, keep = {} }: { path: string; categories: string[]; category: string; keep?: Record<string, string> }) {
  return (
    <nav className="filters" aria-label="Filter by category">
      <Link to={addressOf(path, keep)} stay aria-current={category === "" ? "true" : undefined}>
        All
      </Link>
      {categories.map((name) => (
        <Link key={name} to={addressOf(path, { ...keep, category: name })} stay aria-current={name === category ? "true" : undefined}>
          {words(name)}
        </Link>
      ))}
      <AddCategory />
    </nav>
  );
}
