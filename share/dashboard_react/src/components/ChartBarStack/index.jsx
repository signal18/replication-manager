import React, { useEffect, useRef, useState } from 'react';
import { Box } from '@chakra-ui/react';
import * as d3 from 'd3';
import styles from '../../styles/_chartbarstack.module.scss';

function ChartBarStack({
  context,
  className,
  metricPaths = [],
  title = "Memory Usage",
  height = 400
}) {
  const chartRef = useRef(null);
  const svgRef = useRef(null);
  const colorScale = d3.scaleOrdinal(d3.schemeCategory10);
  const [metricsData, setMetricsData] = useState({});
  const abortControllerRef = useRef(new AbortController());
  const dataTimestampRef = useRef(null);
  const [dataKey, setDataKey] = useState(0); // Used to track data updates

  const getDisplayName = (metricPath) => {
    const parts = metricPath.split('.');
    let displayName = parts[parts.length - 1] || metricPath;
    displayName = displayName.split('_').slice(3).join('_');
    return displayName.replace(/\)+$/, '') || metricPath;
  };

  const formatWithUnits = (value) => {
    if (value <= 0) return '0';
    const k = 1024;
    const sizes = ['', 'K', 'M', 'G', 'T'];
    const i = Math.floor(Math.log(value) / Math.log(k));
    return d3.format(',.1f')(value / Math.pow(k, i)) + sizes[i];
  };

  const fetchMetricData = async (metricPath) => {
    try {
      const now = Math.floor(Date.now() / 1000);
      const step = context.step() / 1000;
      const size = context.size();
      const from = now - (size * step);
      const until = now;
      const url = `/graphite/render?format=raw&target=${encodeURIComponent(`alias(${metricPath},'')`)}&from=${from}&until=${until}`;

      const response = await fetch(url, {
        signal: abortControllerRef.current.signal
      });

      if (!response.ok) throw new Error(`HTTP error ${response.status}`);

      const text = await response.text();
      const [meta, values] = text.split('|');
      const [_, start, end, stepSize] = meta.split(',');

      // Process values with proper handling of None values
      const processedValues = values.split(',').map((v, i) => {
        const timestamp = (parseInt(start) + i * parseInt(stepSize)) * 1000;
        const value = v === 'None' ? 0 : parseFloat(v) || 0;
        return {
          date: new Date(timestamp),
          value: value
        };
      });

      return {
        path: metricPath,
        data: processedValues
      };
    } catch (error) {
      if (error.name !== 'AbortError') {
        console.error('Fetch error:', error);
      }
      return { path: metricPath, data: [] };
    }
  };

  const fetchAllMetrics = async () => {
    try {
      const data = await Promise.all(
        metricPaths.map(path => fetchMetricData(path))
      );

      const dataMap = data.reduce((acc, curr) => {
        acc[curr.path] = curr;
        return acc;
      }, {});

      // Only update if data has actually changed
      const newDataTimestamp = Date.now();
      dataTimestampRef.current = newDataTimestamp;

      setMetricsData(prevData => {
        // Compare data to see if it's significantly different
        const hasSignificantChanges = Object.keys(dataMap).some(path => {
          const prevValues = prevData[path]?.data?.map(d => d.value).join(',');
          const newValues = dataMap[path]?.data?.map(d => d.value).join(',');
          return prevValues !== newValues;
        });

        if (hasSignificantChanges) {
          setDataKey(prevKey => prevKey + 1);
          return dataMap;
        }
        return prevData;
      });
    } catch (error) {
      if (error.name !== 'AbortError') console.error(error);
    }
  };

  useEffect(() => {
    // Setup fetch interval
    if (!metricPaths.length) return;

    // Cancel previous requests
    abortControllerRef.current.abort();
    abortControllerRef.current = new AbortController();

    // Initial fetch
    fetchAllMetrics();

    // Setup polling interval (less frequent than animation frames)
    const intervalId = setInterval(fetchAllMetrics, 10000); // Poll every 10 seconds

    return () => {
      clearInterval(intervalId);
      abortControllerRef.current.abort();
    };
  }, [metricPaths, context]);

  // Separate effect for drawing
  useEffect(() => {
    if (Object.keys(metricsData).length > 0) {
      drawChart(metricsData);
    }
  }, [metricsData, dataKey]);

  const drawChart = (dataMap) => {
    const container = chartRef.current;
    if (!container) return;

    // Validate data
    const primaryData = dataMap[metricPaths[0]]?.data;
    if (!primaryData || !primaryData.length) return;

    // Clear previous chart
    d3.select(container).selectAll('svg').remove();

    // Dimensions
    const margin = { top: 60, right: 30, bottom: 50, left: 60 };
    const width = container.clientWidth - margin.left - margin.right;
    const chartHeight = height - margin.top - margin.bottom;

    // Create SVG
    const svg = d3.select(container).append('svg')
      .attr('width', '100%')
      .attr('height', height)
      .style('shape-rendering', 'crispEdges');

    svgRef.current = svg;

    const g = svg.append('g')
      .attr('transform', `translate(${margin.left},${margin.top})`);

    // Process data - ensure all metrics have values for all timestamps
    const allDates = primaryData.map(d => d.date);
    const processedData = allDates.map(date => {
      const entry = { date };
      metricPaths.forEach(path => {
        const pointData = dataMap[path]?.data.find(d =>
          d.date.getTime() === date.getTime()
        );
        entry[path] = pointData?.value || 0;
      });
      return entry;
    });

    // Stack layout
    const stack = d3.stack().keys(metricPaths);
    const stackedData = stack(processedData);

    // Scales
    const xScale = d3.scaleTime()
      .domain(d3.extent(allDates))
      .range([0, width])
      .nice();

    const maxTotal = d3.max(processedData, d =>
      metricPaths.reduce((sum, path) => sum + d[path], 0)
    );

    const yScale = d3.scaleLinear()
      .domain([0, maxTotal || 1]) // Fallback to 1 if maxTotal is 0
      .nice()
      .range([chartHeight, 0]);

    // Bar width calculation
    const barWidth = Math.max(
      0.5, // Minimum width
      Math.min(
        width / processedData.length * 0.9,
        20 // Maximum width
      )
    );

    // Draw bars with transition
    g.selectAll('.layer')
      .data(stackedData)
      .enter().append('g')
      .attr('class', styles.layer)
      .attr('fill', d => colorScale(d.key))
      .selectAll('rect')
      .data(d => d)
      .enter().append('rect')
      .attr('x', d => xScale(d.data.date) - barWidth/2)
      .attr('width', barWidth)
      .attr('y', d => yScale(d[1]))
      .attr('height', d => yScale(d[0]) - yScale(d[1]));

    // Totals with conditional rendering
    processedData.forEach(d => {
      const total = metricPaths.reduce((sum, path) => sum + d[path], 0);
      if (total > 0 && barWidth > 10) {
        g.append('text')
          .attr('x', xScale(d.date))
          .attr('y', yScale(total) - 5)
          .attr('text-anchor', 'middle')
          .attr('class', styles.totalLabel)
          .text(formatWithUnits(total));
      }
    });

    // Axes
    g.append('g')
      .attr('transform', `translate(0,${chartHeight})`)
      .attr('class', styles.axis)
      .call(d3.axisBottom(xScale).ticks(5));

    g.append('g')
      .attr('class', styles.axis)
      .call(d3.axisLeft(yScale).ticks(5).tickFormat(formatWithUnits));

    // Legend with optimized positioning
    const legend = g.append('g')
      .attr('transform', `translate(0, -40)`);

    let xOffset = 0;
    metricPaths.forEach((path, i) => {
      const legendItem = legend.append('g')
        .attr('transform', `translate(${xOffset}, 0)`);

      legendItem.append('rect')
        .attr('width', 12)
        .attr('height', 12)
        .attr('fill', colorScale(path));

      const text = legendItem.append('text')
        .attr('x', 18)
        .attr('y', 10)
        .text(getDisplayName(path))
        .attr('class', styles.legendText);

      // Calculate text width for spacing
      const textWidth = text.node().getComputedTextLength();
      xOffset += textWidth + 30; // 12 (rect) + 18 (spacing)
    });
  };

  return (
    <div className={styles.container}>
      {title && <h3 className={styles.title}>{title}</h3>}
      <Box
        ref={chartRef}
        className={`${styles.chartContainer} ${className || ''}`}
        width="100%"
        height={height}
        data-key={dataKey} // Add key to help React track changes
      />
    </div>
  );
}

export default ChartBarStack;
