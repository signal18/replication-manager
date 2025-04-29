import React, { useEffect, useRef, useState } from 'react';
import { Box } from '@chakra-ui/react';
import * as d3 from 'd3';
import styles from '../../styles/_chartbarstack.module.scss';
import { useTheme } from '../../ThemeProvider';

function ChartLatchTracing({
  context,
  className,
  metricPaths = [],
  title = "Performance Schema Stats",
  height = 400,
  isVisible = true
}) {
  const chartRef = useRef(null);
  const svgRef = useRef(null);
  const colorScale = d3.scaleOrdinal(d3.schemeCategory10);
  const [metricsData, setMetricsData] = useState({});
  const abortControllerRef = useRef(new AbortController());
  const dataTimestampRef = useRef(null);
  const [dataKey, setDataKey] = useState(0);
  const { theme } = useTheme();

  // Theme variables
  const themeColors = {
    background: theme === 'light' ? 'rgba(245, 247, 250, 0.7)' : 'rgba(30, 34, 39, 0.7)',
    text: theme === 'light' ? '#333333' : '#e2e8f0',
    axis: theme === 'light' ? '#718096' : '#718096',
    axisText: theme === 'light' ? '#4A5568' : '#a0aec0',
    gridLines: theme === 'light' ? '#e2e8f0' : '#2d3748'
  };

  // Custom label parsing logic - with specific formatting for mysql metrics
  const getDisplayName = (metricPath) => {
    // Extract the last part of the metric path
    const parts = metricPath.split('.');
    let displayName = parts[parts.length - 1] || metricPath;

    // Parse the display name - cut first 6 words and last word if separated by underscore
    const words = displayName.split('_');
    if (words.length > 7) { // Only process if we have enough segments
      // Take words starting from index 6 (7th word) up to second-to-last word
      displayName = words.slice(6, -1).join('_');
    }

    // Remove leading spaces and trailing parentheses
    displayName = displayName.trim().replace(/\)+$/, '');

    return displayName || metricPath;
  };

  const formatWithUnits = (value) => {
    if (value <= 0) return '0';
    const k = 1000; // Using 1000 instead of 1024 if your metrics are decimal-based
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

      // Process values with proper handling of None values - always convert to 0
      const processedValues = values.split(',').map((v, i) => {
        const timestamp = (parseInt(start) + i * parseInt(stepSize)) * 1000;
        // Replace None with 0 and ensure parseFloat fallback to 0 for any invalid values
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

      // Normalize data - ensure consistent timestamps across all metrics
      // This helps prevent gaps when some metrics have None values
      let allTimestamps = new Set();

      // First collect all unique timestamps
      data.forEach(metricData => {
        metricData.data.forEach(point => {
          allTimestamps.add(point.date.getTime());
        });
      });

      // Sort timestamps chronologically
      const sortedTimestamps = Array.from(allTimestamps).sort();

      // Create a normalized data map with values for all timestamps
      const normalizedData = {};

      data.forEach(metricData => {
        const path = metricData.path;
        const pointsMap = {};

        // Create a map of existing data points
        metricData.data.forEach(point => {
          pointsMap[point.date.getTime()] = point.value;
        });

        // Create a complete dataset with all timestamps
        normalizedData[path] = {
          path: path,
          data: sortedTimestamps.map(timestamp => {
            return {
              date: new Date(timestamp),
              // Use 0 for any missing timestamp
              value: pointsMap[timestamp] !== undefined ? pointsMap[timestamp] : 0
            };
          })
        };
      });

      // Only update if data has actually changed
      const newDataTimestamp = Date.now();
      dataTimestampRef.current = newDataTimestamp;

      setMetricsData(prevData => {
        // Compare data to see if it's significantly different
        const hasSignificantChanges = Object.keys(normalizedData).some(path => {
          const prevValues = prevData[path]?.data?.map(d => d.value).join(',');
          const newValues = normalizedData[path]?.data?.map(d => d.value).join(',');
          return prevValues !== newValues;
        });

        if (hasSignificantChanges) {
          setDataKey(prevKey => prevKey + 1);
          return normalizedData;
        }
        return prevData;
      });
    } catch (error) {
      if (error.name !== 'AbortError') console.error(error);
    }
  };

  useEffect(() => {
    // Only run if component is visible and there are metrics
    if (!isVisible || !metricPaths.length) return;

    // Cancel previous requests
    abortControllerRef.current.abort();
    abortControllerRef.current = new AbortController();

    // Initial fetch
    fetchAllMetrics();

    // Setup polling interval
    const intervalId = setInterval(fetchAllMetrics, 10000); // Poll every 10 seconds

    return () => {
      clearInterval(intervalId);
      abortControllerRef.current.abort();
    };
  }, [metricPaths, context, isVisible]);

  // Separate effect for drawing - now includes theme
  useEffect(() => {
    if (isVisible && Object.keys(metricsData).length > 0) {
      drawChart(metricsData);
    }
  }, [metricsData, dataKey, isVisible, theme]); // Added theme dependency

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

    // Create SVG with theming
    const svg = d3.select(container).append('svg')
      .attr('width', '100%')
      .attr('height', height)
      .style('shape-rendering', 'crispEdges')
      .style('background', 'transparent'); // Transparent background to let container color show

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

    // Add grid lines with theme-based styling
    g.append('g')
      .attr('class', 'grid')
      .attr('transform', `translate(0,${chartHeight})`)
      .call(
        d3.axisBottom(xScale)
          .ticks(5)
          .tickSize(-chartHeight)
          .tickFormat('')
      )
      .call(g => g.selectAll('.tick line')
        .style('stroke', themeColors.gridLines)
        .style('stroke-opacity', 0.5)
        .style('shape-rendering', 'crispEdges'))
      .call(g => g.select('.domain').remove());

    g.append('g')
      .attr('class', 'grid')
      .call(
        d3.axisLeft(yScale)
          .ticks(5)
          .tickSize(-width)
          .tickFormat('')
      )
      .call(g => g.selectAll('.tick line')
        .style('stroke', themeColors.gridLines)
        .style('stroke-opacity', 0.5)
        .style('shape-rendering', 'crispEdges'))
      .call(g => g.select('.domain').remove());

    // Totals with conditional rendering
    processedData.forEach(d => {
      const total = metricPaths.reduce((sum, path) => sum + d[path], 0);
      if (total > 0 && barWidth > 10) {
        g.append('text')
          .attr('x', xScale(d.date))
          .attr('y', yScale(total) - 5)
          .attr('text-anchor', 'middle')
          .style('fill', themeColors.text)
          .style('font-size', '10px')
          .text(formatWithUnits(total));
      }
    });

    // Axes - styled for theme
    g.append('g')
      .attr('transform', `translate(0,${chartHeight})`)
      .call(d3.axisBottom(xScale).ticks(5))
      .call(g => g.selectAll('.domain, .tick line')
        .style('stroke', themeColors.axis))
      .call(g => g.selectAll('.tick text')
        .style('fill', themeColors.axisText));

    g.append('g')
      .call(d3.axisLeft(yScale).ticks(5).tickFormat(formatWithUnits))
      .call(g => g.selectAll('.domain, .tick line')
        .style('stroke', themeColors.axis))
      .call(g => g.selectAll('.tick text')
        .style('fill', themeColors.axisText));

    // Legend with optimized positioning - styled for theme
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
        .style('font-size', '12px')
        .style('fill', themeColors.text);

      // Calculate text width for spacing
      const textWidth = text.node().getComputedTextLength();
      xOffset += textWidth + 30; // 12 (rect) + 18 (spacing)
    });

    // Add title
    if (title) {
      svg.append('text')
        .attr('x', container.clientWidth / 2)
        .attr('y', 20)
        .attr('text-anchor', 'middle')
        .style('fill', themeColors.text)
        .style('font-size', '16px')
        .style('font-weight', 'bold')
        .text(title);
    }
  };

  // Early return if not visible
  if (!isVisible) {
    return null;
  }

  return (
    <div className={styles.container}>
      <Box
        ref={chartRef}
        className={`${styles.chartContainer} ${className || ''}`}
        width="100%"
        height={height}
        data-key={dataKey}
        bg={themeColors.background}
        borderRadius="md"
        p={3}
        boxShadow="lg"
      />
    </div>
  );
}

export default ChartLatchTracing;
