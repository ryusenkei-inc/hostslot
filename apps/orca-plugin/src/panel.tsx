// The details panel in Orca's right sidebar. Orca strips the types, compiles
// the JSX and provides the `orca` module; the worker posts the view on each
// poll, so the panel only draws it.
import {
  atom,
  Button,
  Callout,
  Collapsible,
  EmptyState,
  Heading,
  KeyValue,
  Link,
  List,
  ListItem,
  plugin,
  Progress,
  read,
  RelativeTime,
  Row,
  set,
  Stack,
  Table,
  Text,
  type PanelContext,
} from 'orca';
import type { View } from './view';

const current = atom<View | null>(null);
const installLink = 'https://github.com/ryusenkei-inc/hostslot#quick-start';

export function setup(ctx: PanelContext): void {
  ctx.subscriptions.push(plugin.on('view', (v: View) => set(current, v)));
  void plugin
    .request('view.get', {})
    .then((v: View) => set(current, v))
    .catch(() => undefined);
}

function Machine({ v }: { v: View }) {
  if (!v.machine) return null;
  const { memory, slots } = v.machine;
  return (
    <Stack gap="sm">
      <Progress label={memory.label} value={memory.usedPct} />
      {memory.note ? (
        <Text size="sm" muted tone={memory.tone === 'neutral' ? undefined : memory.tone}>
          {memory.note}
        </Text>
      ) : null}
      <Progress label={slots.label} value={slots.usedPct} />
    </Stack>
  );
}

function Running({ v }: { v: View }) {
  if (!v.machine) return null;
  return (
    <Stack gap="sm">
      <Heading level={2}>Running</Heading>
      {v.running.length === 0 ? (
        <Text muted>Nothing running. Heavy commands start at once.</Text>
      ) : (
        <List label="Running">
          {v.running.map((r) => (
            <ListItem key={r.key} title={r.title} description={r.detail}>
              <Row gap="sm" wrap>
                <Text size="sm">{r.memory}</Text>
                <Text size="sm" muted>
                  <RelativeTime at={r.since} format="duration" muted />
                </Text>
                {r.left ? (
                  <Text size="sm" tone={r.left === 'longer than usual' ? 'warning' : undefined} muted={r.left !== 'longer than usual'}>
                    {r.left}
                  </Text>
                ) : null}
              </Row>
            </ListItem>
          ))}
        </List>
      )}
    </Stack>
  );
}

function Waiting({ v }: { v: View }) {
  if (v.waiting.length === 0) return null;
  return (
    <Stack gap="sm">
      <Heading level={2}>Waiting</Heading>
      <List label="Waiting">
        {v.waiting.map((w) => (
          <ListItem key={w.key} title={`#${w.position} ${w.title}`} description={w.detail}>
            <Row gap="sm" wrap>
              <Text size="sm" tone="warning">
                {w.why}
              </Text>
              <Text size="sm" muted>
                waited <RelativeTime at={w.since} format="duration" muted />
              </Text>
              {w.eta ? (
                <Text size="sm" muted>
                  starts in {w.eta}
                </Text>
              ) : null}
            </Row>
          </ListItem>
        ))}
      </List>
    </Stack>
  );
}

function DevServers({ v }: { v: View }) {
  if (v.ports.length === 0) return null;
  const running = v.ports.filter((p) => p.running);
  const idle = v.ports.filter((p) => !p.running);
  return (
    <Stack gap="sm">
      <Heading level={2}>Dev servers</Heading>
      {running.length === 0 ? (
        <Text muted>None running.</Text>
      ) : (
        <List label="Dev servers">
          {running.map((p) => (
            <ListItem key={p.key} title={p.name} description={p.where}>
              <Row gap="sm" wrap>
                <Link href={p.url}>{p.url}</Link>
                {p.note ? (
                  <Text size="sm" tone="warning">
                    {p.note}
                  </Text>
                ) : null}
              </Row>
            </ListItem>
          ))}
        </List>
      )}
      {idle.length > 0 ? (
        <Collapsible title="Assigned ports" description={`${idle.length} not running`}>
          <KeyValue label="Assigned ports" items={idle.map((p) => ({ label: `${p.name} · ${p.where}`, value: p.url.replace('http://localhost:', '') }))} />
        </Collapsible>
      ) : null}
    </Stack>
  );
}

// A summary per guard; the stopped jobs themselves are rows of Recent.
function Stops({ v }: { v: View }) {
  if (v.stops.length === 0) return null;
  return (
    <Callout tone="danger" title="Stopped by a guard">
      <Stack gap="md">
        {v.stops.map((g) => (
          <Stack key={g.key} gap="xs">
            <Text weight="semibold">{g.title}</Text>
            <Text size="sm">
              {g.summary} · last <RelativeTime at={g.last} format="short" />
            </Text>
            <Text size="sm" muted>
              {g.explanation}
            </Text>
          </Stack>
        ))}
      </Stack>
    </Callout>
  );
}

function Recent({ v }: { v: View }) {
  if (v.recent.length === 0) return null;
  return (
    <Collapsible title="Recent" description={`${v.recent.length} ended`} defaultOpen>
      <Table
        label="Recent jobs"
        columns={[
          { key: 'title', label: 'Command', minWidth: 'sm' },
          { key: 'outcome', label: 'Result', minWidth: 'xs' },
          { key: 'where', label: 'Where', hideBelow: 'md' },
          { key: 'peak', label: 'Peak', align: 'end', hideBelow: 'sm' },
          { key: 'took', label: 'Took', align: 'end', hideBelow: 'md' },
          { key: 'ended', label: 'Ended', align: 'end' },
        ]}
        rows={v.recent.map((r) => ({
          key: r.key,
          cells: {
            title: r.title,
            outcome: r.outcome,
            where: r.where,
            peak: r.peak,
            took: r.took,
            ended: { type: 'relative', at: r.ended, format: 'short' },
          },
        }))}
      />
    </Collapsible>
  );
}

function Repos({ v }: { v: View }) {
  if (v.byRepo.length < 2) return null;
  return (
    <Collapsible title="By repository" description={`${v.byRepo.length} repositories`}>
      <KeyValue label="Jobs by repository" items={v.byRepo.map((r) => ({ label: r.repo, value: r.summary }))} />
    </Collapsible>
  );
}

// Problems in brief, then one button for what can be fixed from here. Nothing
// is downloaded or written until it is pressed; its note says what it does.
function Setup({ v }: { v: View }) {
  const { problems, action, progress, result, facts, checks } = v.setup;
  const busy = Boolean(progress);
  return (
    <Stack gap="sm">
      {result ? (
        <Callout tone={result.ok ? 'success' : 'danger'} title={result.title}>
          {result.items.length > 0 ? <KeyValue label={result.title} items={result.items} /> : null}
          {result.text ? <Text size="sm">{result.text}</Text> : null}
        </Callout>
      ) : null}
      {result?.next ? (
        <Callout tone="warning" title="One more step">
          <Text size="sm">{result.next}</Text>
        </Callout>
      ) : null}
      {problems.length > 0 ? (
        <Callout tone={problems.some((p) => p.tone === 'danger') ? 'danger' : 'warning'} title="Setup">
          <List label="Setup problems">
            {problems.map((p) => (
              <ListItem key={p.key} title={p.title} description={p.detail} />
            ))}
          </List>
        </Callout>
      ) : null}
      {action ? (
        <Stack gap="xs">
          <Button variant="solid" tone="accent" disabled={busy} onPress={() => void plugin.request(action.request, {})}>
            {action.label}
          </Button>
          <Text size="sm" muted>
            {action.note}
          </Text>
        </Stack>
      ) : null}
      {progress ? (
        <Text size="sm" muted>
          {progress}
        </Text>
      ) : null}
      {!v.machine ? <Link href={installLink}>Other ways to install</Link> : null}
      {problems.length === 0 && v.machine ? (
        <Collapsible title="Setup" description={checks > 0 ? `All ${checks} checks pass` : 'Not checked yet'}>
          <KeyValue label="Hostslot" items={facts} />
        </Collapsible>
      ) : null}
    </Stack>
  );
}

export default function Panel() {
  const v = read(current);
  if (!v) return <EmptyState title="Reading Hostslot status…" />;
  return (
    <Stack gap="md">
      <Heading>{v.headline}</Heading>
      <Machine v={v} />
      <Running v={v} />
      <Waiting v={v} />
      <DevServers v={v} />
      <Stops v={v} />
      <Recent v={v} />
      <Repos v={v} />
      <Setup v={v} />
    </Stack>
  );
}
