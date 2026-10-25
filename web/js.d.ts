// For the check of the JavaScript (jsconfig.json, scripts/test.sh js). The pages find their elements by selector
// and read what they put there (value, href, open, dataset), so elements, events and their targets take any
// property: the check is for names, imports and calls, not for which kind of element a selector finds.
interface Element { [name: string]: any }
interface EventTarget { [name: string]: any }
interface Event { [name: string]: any }

interface Window {
  __editor?: any; // the editor, for the browser tests and the console (editor.js)
}
