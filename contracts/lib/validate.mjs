// A dependency-free JSON Schema (draft 2020-12 subset) validator used by the
// executable protocol contract. Supported keywords: $ref (local and relative
// file refs), type, required, properties, additionalProperties, items,
// enum, const, pattern, minLength, maxLength, minimum, maximum, minItems,
// maxItems, uniqueItems, oneOf, anyOf, allOf.
//
// This is intentionally small: the contract schemas only use this subset, and
// the verifier reports unsupported keywords instead of silently ignoring them.

import fs from 'node:fs';
import path from 'node:path';

const schemaCache = new Map();

export function loadSchema(file) {
  const absolute = path.resolve(file);
  if (!schemaCache.has(absolute)) {
    schemaCache.set(absolute, JSON.parse(fs.readFileSync(absolute, 'utf8')));
    schemaCache.set(absolute, { ...schemaCache.get(absolute), __file: absolute });
  }
  return schemaCache.get(absolute);
}

function typeOf(value) {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  if (Number.isInteger(value)) return 'integer';
  return typeof value;
}

function matchesType(value, type) {
  const actual = typeOf(value);
  if (type === 'number') return actual === 'number' || actual === 'integer';
  return actual === type;
}

function ptr(base, key) {
  return typeof key === 'number' ? `${base}[${key}]` : `${base}.${key}`;
}

function resolveRef(ref, baseFile) {
  const [filePart, fragment = ''] = ref.split('#');
  const file = filePart === '' ? baseFile : path.resolve(path.dirname(baseFile), filePart);
  const schema = loadSchema(file);
  if (fragment === '') return { schema, file };
  const segments = fragment.replace(/^\//, '').split('/').filter(Boolean);
  let node = schema;
  let currentFile = file;
  for (const raw of segments) {
    const segment = decodeURIComponent(raw).replace(/~1/g, '/').replace(/~0/g, '~');
    node = node?.[segment];
    if (node === undefined) throw new Error(`unresolvable $ref ${ref}`);
    if (typeof node === 'object' && node !== null && typeof node.$ref === 'string') {
      const nested = resolveRef(node.$ref, currentFile);
      node = nested.schema;
      currentFile = nested.file;
    }
  }
  return { schema: node, file: currentFile };
}

export function validate(value, schema, { rootDir = process.cwd(), file = null } = {}) {
  const errors = [];
  const rootFile = file ? path.resolve(file) : path.join(rootDir, 'inline.schema.json');
  walk(value, schema, '$', rootFile, errors, new Set());
  return errors;
}

function walk(value, schema, location, file, errors, refStack) {
  if (schema === true) return;
  if (schema === false) {
    errors.push(`${location}: schema forbids any value`);
    return;
  }
  if (schema === null || typeof schema !== 'object') return;

  let currentFile = file;
  if (typeof schema.$ref === 'string') {
    const resolved = resolveRef(schema.$ref, currentFile);
    const key = `${resolved.file}#${schema.$ref}`;
    if (refStack.has(key)) return; // recursive schemas are exercised by data, not the schema graph
    refStack.add(key);
    walk(value, resolved.schema, location, resolved.file, errors, refStack);
    refStack.delete(key);
    return;
  }

  if (schema.type !== undefined) {
    const types = Array.isArray(schema.type) ? schema.type : [schema.type];
    if (!types.some((type) => matchesType(value, type))) {
      errors.push(`${location}: expected ${types.join('|')}, got ${typeOf(value)}`);
      return;
    }
  }

  if (schema.const !== undefined && JSON.stringify(value) !== JSON.stringify(schema.const)) {
    errors.push(`${location}: expected constant ${JSON.stringify(schema.const)}`);
  }
  if (Array.isArray(schema.enum) && !schema.enum.some((item) => JSON.stringify(item) === JSON.stringify(value))) {
    errors.push(`${location}: value is not one of ${JSON.stringify(schema.enum)}`);
  }

  if (typeof value === 'string') {
    if (schema.minLength !== undefined && [...value].length < schema.minLength) {
      errors.push(`${location}: shorter than ${schema.minLength} characters`);
    }
    if (schema.maxLength !== undefined && [...value].length > schema.maxLength) {
      errors.push(`${location}: longer than ${schema.maxLength} characters`);
    }
    if (schema.pattern !== undefined && !new RegExp(schema.pattern, 'u').test(value)) {
      errors.push(`${location}: does not match /${schema.pattern}/`);
    }
  }

  if (typeof value === 'number') {
    if (schema.minimum !== undefined && value < schema.minimum) {
      errors.push(`${location}: less than ${schema.minimum}`);
    }
    if (schema.maximum !== undefined && value > schema.maximum) {
      errors.push(`${location}: greater than ${schema.maximum}`);
    }
  }

  if (Array.isArray(value)) {
    if (schema.minItems !== undefined && value.length < schema.minItems) {
      errors.push(`${location}: fewer than ${schema.minItems} items`);
    }
    if (schema.maxItems !== undefined && value.length > schema.maxItems) {
      errors.push(`${location}: more than ${schema.maxItems} items`);
    }
    if (schema.uniqueItems === true) {
      const seen = new Set();
      for (const item of value) {
        const key = JSON.stringify(item);
        if (seen.has(key)) errors.push(`${location}: duplicate array item ${key}`);
        seen.add(key);
      }
    }
    if (schema.items !== undefined) {
      value.forEach((item, index) => walk(item, schema.items, ptr(location, index), currentFile, errors, refStack));
    }
  }

  if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
    for (const required of schema.required ?? []) {
      if (!Object.prototype.hasOwnProperty.call(value, required)) {
        errors.push(`${location}: missing required property "${required}"`);
      }
    }
    const properties = schema.properties ?? {};
    const known = new Set(Object.keys(properties));
    for (const [key, item] of Object.entries(value)) {
      const child = ptr(location, key);
      if (known.has(key)) {
        walk(item, properties[key], child, currentFile, errors, refStack);
      } else if (schema.additionalProperties === false) {
        errors.push(`${child}: unknown property`);
      } else if (schema.additionalProperties && typeof schema.additionalProperties === 'object') {
        walk(item, schema.additionalProperties, child, currentFile, errors, refStack);
      }
    }
  }

  if (Array.isArray(schema.allOf)) {
    for (const item of schema.allOf) walk(value, item, location, currentFile, errors, refStack);
  }
  if (Array.isArray(schema.anyOf)) {
    const attempts = schema.anyOf.map((item) => {
      const nested = [];
      walk(value, item, location, currentFile, nested, refStack);
      return nested;
    });
    if (attempts.every((nested) => nested.length > 0)) {
      errors.push(`${location}: does not match anyOf (${attempts.map((nested) => nested.join('; ')).join(' | ')})`);
    }
  }
  if (Array.isArray(schema.oneOf)) {
    const matches = [];
    const attempts = [];
    schema.oneOf.forEach((item) => {
      const nested = [];
      walk(value, item, location, currentFile, nested, refStack);
      attempts.push(nested);
      if (nested.length === 0) matches.push(item);
    });
    if (matches.length !== 1) {
      errors.push(`${location}: matched ${matches.length} oneOf branches`);
    }
  }

  for (const keyword of Object.keys(schema)) {
    if (!KNOWN_KEYWORDS.has(keyword) && !keyword.startsWith('$') && !keyword.startsWith('__')) {
      errors.push(`${location}: schema uses unsupported keyword "${keyword}"`);
    }
  }
}

const KNOWN_KEYWORDS = new Set([
  '$schema', '$id', '$defs', '$ref', '$comment', 'title', 'description', 'default', 'examples',
  'type', 'required', 'properties', 'additionalProperties', 'items', 'enum', 'const',
  'pattern', 'minLength', 'maxLength', 'minimum', 'maximum', 'minItems', 'maxItems',
  'uniqueItems', 'oneOf', 'anyOf', 'allOf', 'format',
]);
