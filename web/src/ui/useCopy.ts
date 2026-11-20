import { useToast } from "./overlays";

// useCopy puts text on the clipboard, and says when the browser did not let the page.
export function useCopy() {
  const toast = useToast();
  return (text: string, what: string) =>
    void navigator.clipboard?.writeText(text).then(
      () => toast("success", `Copied ${what}.`),
      () => toast("warning", "The browser did not let this page copy. Select the text and copy it."),
    );
}
