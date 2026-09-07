import React, { useEffect, useRef, useState } from 'react'
import * as d3 from 'd3'
import { Box, Text } from '@chakra-ui/react'
import { useTheme } from '../../ThemeProvider'

// Live time-series line chart (d3) matching the sysbench BenchCompareModal LineChart look,
// fed from graphite /render?format=raw. Supports a LOG Y scale with a fixed cap (the axis
// spans [1, cap] so the full dynamic range is always visible), and multiple series drawn on
// the same axes (e.g. network in/out). Polls every refreshMs over [now-windowSec, now].
//
//   targets: [{ target: '<graphite target>', label, color? }]
function ChartTimeSeriesLine({
  title,
  targets = [],
  yLabel = '',
  logScale = false,
  logBase = 10,
  cap = 0,
  yTickValues = null,   // explicit Y tick positions (e.g. natural time boundaries)
  yTickFormat = null,   // custom Y tick formatter (e.g. seconds -> "1m"/"1h"/"1d")
  height = 220,
  windowSec = 3600,
  refreshMs = 10000,
  className,
}) {
  const { theme } = useTheme()
  const wrapRef = useRef(null)
  const svgRef = useRef(null)
  const [series, setSeries] = useState([])
  const [w, setW] = useState(800)

  // key that only changes when the actual targets change (avoids refetch churn on every render)
  const targetsKey = JSON.stringify(targets.map((t) => t.target))

  // Responsive width
  useEffect(() => {
    if (!wrapRef.current || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver((entries) => {
      const cw = entries[0]?.contentRect?.width
      if (cw && cw > 0) setW(Math.floor(cw))
    })
    ro.observe(wrapRef.current)
    return () => ro.disconnect()
  }, [])

  // Fetch loop
  useEffect(() => {
    let alive = true
    const fetchAll = async () => {
      const until = Math.floor(Date.now() / 1000)
      const from = until - windowSec
      try {
        const results = await Promise.all(
          targets.map(async (t) => {
            const params = new URLSearchParams()
            params.set('format', 'raw')
            params.set('from', String(from))
            params.set('until', String(until))
            params.set('target', `alias(${t.target},'')`)
            params.set('noCache', '1')
            const res = await fetch(`/graphite/render?${params.toString()}`)
            if (!res.ok) return null
            const text = await res.text()
            if (!text.trim()) return null
            // raw format is one "name,start,end,step|v,v,..." line per series; a wildcard
            // target that wasn't aggregated returns several -- take the first rather than blank.
            const rawLine = text.trim().split('\n').find((l) => l.includes('|'))
            if (!rawLine) return null
            const parts = rawLine.split('|')
            if (parts.length !== 2) return null
            const ti = parts[0].split(',')
            const start = parseInt(ti[1])
            const step = parseInt(ti[3])
            if (!Number.isFinite(start) || !Number.isFinite(step) || step <= 0) return null
            const data = parts[1].split(',').map((v, j) => ({
              x: new Date((start + j * step) * 1000),
              y: v === 'None' ? null : parseFloat(v),
            }))
            return { label: t.label, color: t.color, data }
          })
        )
        if (alive) setSeries(results.filter(Boolean))
      } catch (e) {
        // graphite hiccup -> keep the last series rather than blank the chart
      }
    }
    fetchAll()
    const id = setInterval(fetchAll, refreshMs)
    return () => {
      alive = false
      clearInterval(id)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [targetsKey, windowSec, refreshMs])

  // Draw
  useEffect(() => {
    const svg = d3.select(svgRef.current)
    svg.selectAll('*').remove()

    const palette = d3.schemeCategory10
    const textColor = theme === 'dark' ? '#e2e8f0' : '#333'
    const gridColor = theme === 'dark' ? 'rgba(255,255,255,0.10)' : 'rgba(0,0,0,0.10)'
    const margin = { top: 22, right: 110, bottom: 26, left: 56 }
    const width = Math.max(180, w) - margin.left - margin.right
    const chartH = height - margin.top - margin.bottom

    const g = svg.append('g').attr('transform', `translate(${margin.left},${margin.top})`)

    const valid = series.filter((s) => s.data && s.data.some((d) => d.y != null))
    if (valid.length === 0) {
      g.append('text').attr('x', width / 2).attr('y', chartH / 2)
        .attr('text-anchor', 'middle').style('fill', textColor, 'important').attr('opacity', 0.5)
        .attr('font-size', '11px').text('no data')
      return
    }

    const allX = valid.flatMap((s) => s.data.map((d) => d.x))
    const x = d3.scaleTime().domain(d3.extent(allX)).range([0, width])

    const maxY = d3.max(valid, (s) => d3.max(s.data, (d) => d.y)) || 1
    let y
    if (logScale) {
      // Fixed span [1, cap] (grows only if data exceeds the cap) -> the full dynamic range
      // is always visible even when the current load sits low.
      y = d3.scaleLog().base(logBase).domain([1, Math.max(cap || 0, maxY, logBase)]).range([chartH, 0]).clamp(true)
    } else {
      y = d3.scaleLinear().domain([0, Math.max(cap || 0, maxY) * 1.05 || 1]).range([chartH, 0])
    }

    // Y grid + axis. For a log scale we set EXPLICIT tick values at powers of the base
    // (1,2,4,8,... for base 2 = natural thread steps; 1,10,100,... for base 10) so the
    // axis stays clean instead of d3's crowded minor log ticks. Base 2 -> integers, else SI.
    const yAxis = d3.axisLeft(y).tickSize(-width)
    const domMax = y.domain()[1] * (1 + 1e-9)
    if (logScale) {
      let ticks = yTickValues
      if (ticks) {
        ticks = ticks.filter((v) => v >= 1 && v <= domMax)
      } else {
        ticks = []
        for (let v = 1; v <= domMax; v *= logBase) ticks.push(v)
      }
      yAxis.tickValues(ticks).tickFormat(yTickFormat || (logBase === 2 ? d3.format('d') : d3.format('~s')))
    } else {
      yAxis.ticks(5).tickFormat(yTickFormat || d3.format('~s'))
    }
    g.append('g')
      .call(yAxis)
      .call((gg) => gg.selectAll('.tick line').attr('stroke', gridColor))
      .call((gg) => gg.selectAll('text').style('fill', textColor, 'important').attr('font-size', '9px'))
      .call((gg) => gg.select('.domain').attr('stroke', textColor))

    // X axis
    g.append('g')
      .attr('transform', `translate(0,${chartH})`)
      .call(d3.axisBottom(x).ticks(6).tickFormat(d3.timeFormat('%H:%M')))
      .call((gg) => gg.selectAll('text').style('fill', textColor, 'important').attr('font-size', '9px'))
      .call((gg) => gg.select('.domain').attr('stroke', textColor))

    const line = d3.line()
      .x((d) => x(d.x))
      .y((d) => y(d.y))
      .defined((d) => d.y != null && (!logScale || d.y > 0))
      .curve(d3.curveMonotoneX)

    valid.forEach((s, i) => {
      g.append('path')
        .datum(s.data)
        .attr('fill', 'none')
        .attr('stroke', s.color || palette[i % palette.length])
        .attr('stroke-width', 1.5)
        .attr('d', line)
    })

    // Legend
    const legend = g.append('g').attr('transform', `translate(${width + 10}, 0)`)
    valid.forEach((s, i) => {
      const row = legend.append('g').attr('transform', `translate(0,${i * 16})`)
      row.append('line').attr('x1', 0).attr('x2', 14).attr('y1', 5).attr('y2', 5)
        .attr('stroke', s.color || palette[i % palette.length]).attr('stroke-width', 1.5)
      row.append('text').attr('x', 18).attr('y', 9).text(s.label)
        .style('fill', textColor, 'important').attr('font-size', '10px')
    })

    if (yLabel) {
      g.append('text').attr('transform', 'rotate(-90)')
        .attr('x', -chartH / 2).attr('y', -44).attr('text-anchor', 'middle')
        .style('fill', textColor, 'important').attr('font-size', '10px').attr('opacity', 0.8).text(yLabel)
    }
  }, [series, theme, w, logScale, cap, height, title, yLabel])

  return (
    <Box className={className} ref={wrapRef} sx={{ width: '100%' }}>
      {title && (
        <Text fontSize='sm' fontWeight='bold' px={2} pt={1}>
          {title}
        </Text>
      )}
      <svg ref={svgRef} width={w} height={height} />
    </Box>
  )
}

export default ChartTimeSeriesLine
