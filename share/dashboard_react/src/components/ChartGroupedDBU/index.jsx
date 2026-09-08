import React, { useEffect, useRef, useState, useCallback } from 'react';
import { Box } from '@chakra-ui/react';
import * as d3 from 'd3';
import { useTheme } from '../../ThemeProvider';

// ChartGroupedDBU renders the per-server DBU picture as REQUESTED (see design):
//   - 4 grouped bars per time bucket, one per resource axis (CPU / Mem / IO / Disk);
//   - each bar splits: SOLID = the real monitored consumption (service_*), translated
//     to DBU by its ratio; PALE = the DBU rounding (the >=1 restart floor, dbu_* - real);
//   - a DBU line ("max line") connecting each bucket's pivot = max of the axes (the
//     billed DBU) -- a max, never a sum;
//   - a horizontal PLAN line = the ceiling the configurator computes today
//     (ceil(max(prov-db-*/ratio))), passed in as planDbu. No plan rework here.
//
// Native units emitted by srv_snd.go: service_cpu=cores, service_mem=bytes,
// service_io=iops, service_disk=bytes. 1 DBU = 1 core / 4 GB / 40 GB / 1000 IOPS,
// so realDBU = service / ratio (below), and dbu_* already carry max(real,1).
const GiB = 1024 * 1024 * 1024;
const AXES = [
  { key: 'cpu', label: 'CPU', ratio: 1, light: '#3f8fd0', dark: '#5aa8e6' },
  { key: 'mem', label: 'Mem', ratio: 4 * GiB, light: '#a21caf', dark: '#d946ef' },
  { key: 'io', label: 'IO', ratio: 1000, light: '#e0603a', dark: '#ef7a54' },
  { key: 'disk', label: 'Disk', ratio: 40 * GiB, light: '#37a06f', dark: '#4dc088' },
];

const TARGET_BUCKETS = 48; // grouped bars stay readable only at a low bucket count -> peak-aggregate

function ChartGroupedDBU({
  context,
  className,
  height = 300,
  title = 'Consumed DBU',
  dbuPaths = {},      // { cpu, mem, io, disk } scoped graphite targets (dbu_* per axis)
  servicePaths = {},  // { cpu, mem, io, disk } scoped graphite targets (service_* per axis)
  pivotPath = '',     // scoped target for the dbu pivot (max line)
  planDbu = 1,        // plan ceiling, as the configurator computes it
  isVisible = true,
}) {
  const chartRef = useRef(null);
  const abortControllerRef = useRef(new AbortController());
  const [series, setSeries] = useState({}); // path -> [{t, v}]
  const [renderTick, setRenderTick] = useState(0);
  const { theme } = useTheme();
  const isDark = theme === 'dark';

  const colors = {
    bg: isDark ? '#2a3048' : '#ffffff',
    text: isDark ? '#e7e9ef' : '#333333',
    grid: isDark ? '#3a4258' : '#e2e8f0',
    axis: isDark ? '#556' : '#cbd5e0',
    dbu: isDark ? '#eabb52' : '#d99a2b',
    plan: isDark ? '#8892b0' : '#6b7495',
    muted: isDark ? '#98a1bd' : '#6b7495',
  };

  const allPaths = useCallback(() => {
    const p = [];
    AXES.forEach((a) => {
      if (dbuPaths[a.key]) p.push(dbuPaths[a.key]);
      if (servicePaths[a.key]) p.push(servicePaths[a.key]);
    });
    if (pivotPath) p.push(pivotPath);
    return p;
  }, [dbuPaths, servicePaths, pivotPath]);

  // --- fetch one graphite raw series (same endpoint/parse as ChartMultiMetric) ---
  const fetchOne = async (metricPath) => {
    try {
      const now = Math.floor(Date.now() / 1000);
      const step = context.step() / 1000;
      const size = context.size();
      const from = now - size * step;
      const url = `/graphite/render?format=raw&target=${encodeURIComponent(`alias(${metricPath},'')`)}&from=${from}&until=${now}`;
      const resp = await fetch(url, { signal: abortControllerRef.current.signal });
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const text = await resp.text();
      const parts = text.split('|');
      if (parts.length !== 2) return { path: metricPath, data: [] };
      const info = parts[0].split(',');
      if (info.length !== 4) return { path: metricPath, data: [] };
      const start = parseInt(info[1]) * 1000;
      const stepMs = parseInt(info[3]) * 1000;
      const data = parts[1].split(',').map((v, i) => ({
        t: start + i * stepMs,
        v: v === 'None' ? NaN : parseFloat(v),
      }));
      return { path: metricPath, data };
    } catch (err) {
      if (err.name !== 'AbortError') console.error(`ChartGroupedDBU fetch ${metricPath}`, err);
      return { path: metricPath, data: [] };
    }
  };

  const fetchAll = useCallback(async () => {
    const paths = allPaths();
    if (!paths.length) return;
    const results = await Promise.all(paths.map(fetchOne));
    if (!chartRef.current || !isVisible) return;
    const map = {};
    results.forEach((r) => { if (r) map[r.path] = r.data; });
    setSeries(map);
    setRenderTick((x) => x + 1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [allPaths, isVisible]);

  useEffect(() => {
    if (!isVisible || !allPaths().length) return;
    abortControllerRef.current.abort();
    abortControllerRef.current = new AbortController();
    fetchAll();
    const id = setInterval(fetchAll, 10000);
    return () => { clearInterval(id); abortControllerRef.current.abort(); };
    // depend on the joined paths BY VALUE (parent rebuilds the objects each render)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [allPaths().join('|'), context, isVisible]);

  // --- build aligned, peak-aggregated display buckets ---
  const buildBuckets = useCallback(() => {
    // reference timeline = the pivot (or first available) series
    const ref = series[pivotPath] || series[dbuPaths.cpu] || Object.values(series)[0] || [];
    const clean = ref.filter((d) => !isNaN(d.v));
    if (!clean.length) return [];
    const N = ref.length;
    const groupBy = Math.max(1, Math.ceil(N / TARGET_BUCKETS));

    const peakAt = (path, from, to, conv) => {
      const arr = series[path];
      if (!arr) return 0;
      let m = 0;
      for (let i = from; i < to && i < arr.length; i++) {
        const raw = arr[i].v;
        if (!isNaN(raw)) { const val = conv ? conv(raw) : raw; if (val > m) m = val; }
      }
      return m;
    };

    const buckets = [];
    for (let g = 0; g * groupBy < N; g++) {
      const from = g * groupBy;
      const to = from + groupBy;
      const t = ref[Math.min(from, N - 1)].t;
      const axes = AXES.map((a) => {
        const billed = peakAt(dbuPaths[a.key], from, to);                       // dbu_* (>=1 floored)
        const real = peakAt(servicePaths[a.key], from, to, (x) => x / a.ratio); // service_* -> DBU
        return { key: a.key, billed, real: Math.min(real, Math.max(billed, real)) };
      });
      const pivot = peakAt(pivotPath, from, to) || Math.max(...axes.map((x) => x.billed), 0);
      buckets.push({ t, axes, pivot });
    }
    return buckets;
  }, [series, dbuPaths, servicePaths, pivotPath]);

  const draw = useCallback(() => {
    const container = chartRef.current;
    if (!container) return;
    d3.select(container).selectAll('svg').remove();

    const buckets = buildBuckets();
    const W = container.clientWidth || 600;
    const margin = { top: 44, right: 16, bottom: 26, left: 40 };
    const plotW = W - margin.left - margin.right;
    const plotH = height - margin.top - margin.bottom;

    const svg = d3.select(container).append('svg')
      .attr('width', '100%').attr('height', height)
      .style('background', colors.bg).style('border-radius', '8px');

    // title
    svg.append('text').attr('x', margin.left).attr('y', 18)
      .style('fill', colors.text).style('font-size', '15px').style('font-weight', 600)
      .text(title);

    if (!buckets.length) {
      svg.append('text').attr('x', '50%').attr('y', height / 2).attr('text-anchor', 'middle')
        .style('fill', colors.muted).style('font-size', '13px').text('No data');
      return;
    }

    const maxVal = Math.max(
      planDbu,
      d3.max(buckets, (b) => Math.max(b.pivot, d3.max(b.axes, (a) => Math.max(a.billed, a.real)))) || 1
    ) * 1.1;

    const x = d3.scaleBand().domain(buckets.map((_, i) => i)).range([0, plotW]).padding(0.25);
    const y = d3.scaleLinear().domain([0, maxVal]).nice().range([plotH, 0]);
    const g = svg.append('g').attr('transform', `translate(${margin.left},${margin.top})`);

    // y grid + ticks (integer DBU units)
    const yticks = y.ticks(Math.min(6, Math.ceil(maxVal)));
    g.append('g').call(d3.axisLeft(y).tickValues(yticks).tickSize(-plotW).tickFormat(d3.format('~s')))
      .call((sel) => sel.selectAll('.tick line').style('stroke', colors.axis).style('stroke-opacity', 0.35))
      .call((sel) => sel.selectAll('.tick text').style('fill', colors.muted).style('font-size', '10px'))
      .call((sel) => sel.select('.domain').remove());

    const bw = x.bandwidth() / AXES.length;

    buckets.forEach((b, i) => {
      const gx = x(i);
      b.axes.forEach((ax, j) => {
        const meta = AXES[j];
        const col = isDark ? meta.dark : meta.light;
        const bx = gx + j * bw;
        const billed = Math.max(ax.billed, 0);
        const real = Math.min(ax.real, billed);
        const yBilled = y(billed);
        const yReal = y(real);
        // pale = rounding (billed - real)
        g.append('rect').attr('x', bx + 0.5).attr('y', yBilled)
          .attr('width', Math.max(0, bw - 1)).attr('height', Math.max(0, yReal - yBilled))
          .attr('fill', col).attr('opacity', 0.24);
        // solid = real monitored
        g.append('rect').attr('x', bx + 0.5).attr('y', yReal)
          .attr('width', Math.max(0, bw - 1)).attr('height', Math.max(0, plotH - yReal))
          .attr('fill', col);
      });
    });

    // DBU max line (pivot) — connects the top of the tallest bar per bucket
    const line = d3.line().x((_, i) => x(i) + x.bandwidth() / 2).y((d) => y(d.pivot)).curve(d3.curveMonotoneX);
    g.append('path').datum(buckets).attr('fill', 'none')
      .attr('stroke', colors.dbu).attr('stroke-width', 2).attr('d', line);

    // PLAN line (configurator ceiling)
    if (planDbu > 0) {
      const yp = y(planDbu);
      g.append('line').attr('x1', 0).attr('x2', plotW).attr('y1', yp).attr('y2', yp)
        .attr('stroke', colors.plan).attr('stroke-width', 1.5).attr('stroke-dasharray', '5 3');
      g.append('text').attr('x', plotW).attr('y', yp - 4).attr('text-anchor', 'end')
        .style('fill', colors.plan).style('font-size', '10px').style('font-weight', 600)
        .text(`plan ${planDbu}`);
    }

    // x axis (time)
    const tickEvery = Math.ceil(buckets.length / 6);
    g.append('g').attr('transform', `translate(0,${plotH})`)
      .call(d3.axisBottom(x).tickValues(buckets.map((_, i) => i).filter((i) => i % tickEvery === 0))
        .tickFormat((i) => {
          const d = new Date(buckets[i].t);
          return `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
        }))
      .call((sel) => sel.selectAll('.tick line, .domain').style('stroke', colors.axis))
      .call((sel) => sel.selectAll('.tick text').style('fill', colors.muted).style('font-size', '10px'));

    // legend
    const legend = svg.append('g').attr('transform', `translate(${margin.left},34)`);
    let lx = 0;
    AXES.forEach((a, j) => {
      const col = isDark ? a.dark : a.light;
      const grp = legend.append('g').attr('transform', `translate(${lx},0)`);
      grp.append('rect').attr('width', 10).attr('height', 10).attr('y', -9).attr('fill', col).attr('rx', 2);
      const txt = grp.append('text').attr('x', 14).attr('y', 0).style('fill', colors.muted).style('font-size', '11px').text(a.label);
      lx += 24 + (a.label.length * 7);
      void txt;
    });
    const dbuGrp = legend.append('g').attr('transform', `translate(${lx},0)`);
    dbuGrp.append('line').attr('x1', 0).attr('x2', 16).attr('y1', -4).attr('y2', -4).attr('stroke', colors.dbu).attr('stroke-width', 2.5);
    dbuGrp.append('text').attr('x', 20).attr('y', 0).style('fill', colors.muted).style('font-size', '11px').text('DBU (max)');
  }, [buildBuckets, height, planDbu, title, colors, isDark]);

  useEffect(() => {
    if (!isVisible) return;
    const t = setTimeout(draw, 40);
    return () => clearTimeout(t);
  }, [renderTick, draw, isVisible, theme]);

  useEffect(() => {
    const onResize = () => setRenderTick((x) => x + 1);
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  return (
    <Box className={className} style={{ backgroundColor: colors.bg, borderRadius: '8px' }}>
      <div ref={chartRef} style={{ height: `${height}px`, position: 'relative', overflow: 'hidden' }} />
    </Box>
  );
}

export default ChartGroupedDBU;
