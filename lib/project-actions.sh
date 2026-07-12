#!/bin/bash
# Project file operations — add, delete, validate.

# Append a project entry to the projects file (creates parent dirs).
add_project_to_file() {
  local name="$1" filepath="$2" projects_file="$3"
  mkdir -p "$(dirname "$projects_file")"
  echo "${name}:${filepath}" >> "$projects_file"
}
