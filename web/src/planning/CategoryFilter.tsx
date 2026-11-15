import { addressOf, Link } from "../app/route";
import { words } from "../parts/parts";
import { AddCategory } from "./AddCategory";

// CategoryFilter narrows a list of records to one category, a link each so the address keeps the filter, and
// adds a category beside them. path is the view's own address.
export function CategoryFilter({ path, categories, category }: { path: string; categories: string[]; category: string }) {
  return (
    <nav className="filters" aria-label="Filter by category">
      <Link to={path} stay aria-current={category === "" ? "true" : undefined}>
        All
      </Link>
      {categories.map((name) => (
        <Link key={name} to={addressOf(path, { category: name })} stay aria-current={name === category ? "true" : undefined}>
          {words(name)}
        </Link>
      ))}
      <AddCategory />
    </nav>
  );
}
