// The web UI: projects, their tokens and the catalog. It calls the lab
// control (/_lab), which admits it by the cookie that came with the page.
"use strict";

let selected = null; // the name of the selected project

// call sends a request to the lab control and returns the JSON answer.
async function call(method, path, body) {
  const options = { method, headers: {} };
  if (body !== undefined) {
    options.headers["Content-Type"] = "application/json";
    options.body = JSON.stringify(body);
  }
  const response = await fetch("/_lab" + path, options);
  const text = await response.text();
  const answer = text ? JSON.parse(text) : null;
  if (!response.ok) {
    throw new Error(answer?.error?.message ?? response.statusText);
  }
  return answer;
}

// show runs an action and shows its error, if any.
async function show(action) {
  const error = document.getElementById("error");
  try {
    await action();
    error.hidden = true;
  } catch (failure) {
    error.textContent = failure.message;
    error.hidden = false;
  }
}

// fill replaces the rows of a table body; each row is a list of cells, text
// or elements.
function fill(id, rows) {
  const body = document.getElementById(id);
  body.replaceChildren(...rows.map((cells) => {
    const row = document.createElement("tr");
    for (const cell of cells) {
      const td = document.createElement("td");
      td.append(cell);
      row.append(td);
    }
    return row;
  }));
  return body;
}

// button returns a button that runs an action.
function button(label, action) {
  const element = document.createElement("button");
  element.textContent = label;
  element.addEventListener("click", (event) => {
    event.stopPropagation();
    show(action);
  });
  return element;
}

// field returns the value of a form's field; form.name would be the form's
// own name, not the field's.
function field(form, name) {
  return form.elements.namedItem(name).value;
}

function date(text) {
  return new Date(text).toLocaleString();
}

async function loadStatus() {
  const status = await call("GET", "/status");
  document.getElementById("status").textContent =
    `${status.version}, ${status.state}, platform ${status.platform}, Cloud API ${status.endpoints.cloud}`;
}

async function loadProjects() {
  const projects = await call("GET", "/projects");
  if (!projects.some((project) => project.name === selected)) {
    selected = projects.length ? projects[0].name : null;
  }
  const body = fill("projects", projects.map((project) => [
    project.name,
    String(project.id),
    project.internet ? "yes" : "no",
    date(project.created),
    button("Delete", async () => {
      if (confirm(`Delete the project ${project.name} with everything in it?`)) {
        await call("DELETE", `/projects/${project.name}`);
        await loadProjects();
      }
    }),
  ]));
  projects.forEach((project, index) => {
    const row = body.children[index];
    row.dataset.project = project.name;
    row.classList.toggle("selected", project.name === selected);
    row.addEventListener("click", () => {
      selected = project.name;
      show(loadProjects);
    });
  });
  await loadProject();
}

async function loadProject() {
  const section = document.getElementById("project");
  section.hidden = selected === null;
  if (selected === null) {
    return;
  }
  for (const element of section.querySelectorAll(".project-name")) {
    element.textContent = selected;
  }
  const tokens = await call("GET", `/projects/${selected}/tokens`);
  fill("tokens", tokens.map((token) => [
    token.name,
    token.permission,
    token.prefix + "…",
    date(token.created),
    button("Delete", async () => {
      await call("DELETE", `/projects/${selected}/tokens/${encodeURIComponent(token.name)}`);
      await loadProject();
    }),
  ]));
  const cloud = `/projects/${selected}/cloud/v1`;
  const locations = await call("GET", `${cloud}/locations?per_page=50`);
  fill("locations", locations.locations.map((location) => [
    location.name, location.city, location.network_zone,
  ]));
  const types = await call("GET", `${cloud}/server_types?per_page=50`);
  fill("server-types", types.server_types.map((type) => [
    type.name, String(type.cores), `${type.memory} GB`, `${type.disk} GB`, type.cpu_type,
  ]));
}

document.getElementById("create-project").addEventListener("submit", (event) => {
  event.preventDefault();
  const form = event.target;
  show(async () => {
    selected = (await call("POST", "/projects", { name: field(form, "name") })).name;
    form.reset();
    await loadProjects();
  });
});

document.getElementById("create-token").addEventListener("submit", (event) => {
  event.preventDefault();
  const form = event.target;
  show(async () => {
    const created = await call("POST", `/projects/${selected}/tokens`, {
      name: field(form, "name"),
      permission: field(form, "permission"),
    });
    const secret = document.getElementById("secret");
    secret.querySelector("code").textContent = created.secret;
    secret.hidden = false;
    form.reset();
    await loadProject();
  });
});

show(async () => {
  await loadStatus();
  await loadProjects();
});
