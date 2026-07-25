const base = "/virtualme/";

export function url(path = "") {
  return `${base}${path.replace(/^\/+/, "")}`;
}

export function canonical(path = "") {
  const clean = path.replace(/^\/+/, "");
  return `https://mayanklahiri.github.io${url(clean)}`;
}
