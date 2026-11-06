// The details panel in Orca's right sidebar. Orca strips the types, compiles
// the JSX and provides the `orca` module; the worker posts the view on each
// poll, so the panel only draws it.
import {
  atom,
  Badge,
  Callout,
  EmptyState,
  Heading,
  KeyValue,
  Link,
  List,
  ListItem,
  plugin,
  read,
  RelativeTime,
  Row,
  set,
  Stack,
  Text,
  type PanelContext,
} from 'orca';
import type { JobRow, View } from './view';

const current = atom<View | null>(null);

export function setup(ctx: PanelContext): void {
  ctx.subscriptions.push(plugin.on('view', (v: View) => set(current, v)));
  void plugin
    .request('view.get', {})
    .then((v: View) => set(current, v))
    .catch(() => undefined);
}

function Jobs({ title, rows }: { title: string; rows: JobRow[] }) {
  if (rows.length === 0) return null;
  return (
    <List label={title}>
      {rows.map((r) => (
        <ListItem key={r.key} title={r.title} description={`${r.source} · ${r.memory}`}>
          <RelativeTime at={r.since} format="duration" muted />
        </ListItem>
      ))}
    </List>
  );
}

export default function Panel() {
  const v = read(current);
  if (!v) return <EmptyState title="Reading loadslot status…" />;
  return (
    <Stack gap="md">
      <Row justify="between">
        <Heading>{v.headline}</Heading>
        <Badge tone={v.tone}>{v.facts[0]?.value ?? '–'}</Badge>
      </Row>
      {v.problem ? (
        <Callout tone="warning" title={v.problem.title}>
          <Text muted>{v.problem.detail}</Text>
          <Link href={v.problem.link}>How to install loadslot</Link>
        </Callout>
      ) : null}
      {v.problems.map((p) => (
        <Callout key={p.text} tone={p.level === 'error' ? 'danger' : 'warning'} title={p.level === 'error' ? 'Error' : 'Warning'}>
          <Text>{p.text}</Text>
        </Callout>
      ))}
      {v.facts.length > 0 ? <KeyValue label="Machine" items={v.facts} /> : null}
      <Heading level={2}>Running</Heading>
      {v.running.length > 0 ? <Jobs title="Running" rows={v.running} /> : <Text muted>No heavy command is running.</Text>}
      {v.waiting.length > 0 ? (
        <Stack gap="sm">
          <Heading level={2}>Waiting</Heading>
          <Jobs title="Waiting" rows={v.waiting} />
        </Stack>
      ) : null}
    </Stack>
  );
}
