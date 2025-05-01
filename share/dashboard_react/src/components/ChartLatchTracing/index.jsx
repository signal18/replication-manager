import React, { useEffect, useRef, useState, useCallback } from 'react';
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
  // Use a single render trigger instead of separate dataKey
  const [renderTrigger, setRenderTrigger] = useState(0);
  const { theme } = useTheme();

  // Add refs for tracking chart drawing state
  const isDrawingRef = useRef(false);
  const pendingDrawRef = useRef(false);
  const drawTimeoutRef = useRef(null);

  // Theme variables using the CSS variables from your theme
  const themeColors = {
    // IMPORTANT: Set background to match border color in dark theme to prevent flashing
    background: theme === 'light' ? 'var(--white-color, #ffffff)' : '#2a3048',
    chartBorder: theme === 'light' ? 'var(--white-color, #ffffff)' : '#2a3048',
    text: theme === 'light' ? 'var(--text-color, #333333)' : 'var(--text-color, #e7e9ef)',
    axis: theme === 'light' ? 'var(--gray-color, #e2e8f0)' : '#2a3048',
    axisText: theme === 'light' ? 'var(--text-color, #333333)' : 'var(--text-color, #e7e9ef)',
    gridLines: theme === 'light' ? 'var(--gray-color, #e2e8f0)' : 'var(--gray-color, #2d3748)',
    timeAxisColor: theme === 'light' ? 'var(--text-color, #333333)' : 'var(--darkgray-color, #778899)',
    chartLine: theme === 'light' ? 'var(--secondary-color, #3182ce)' : 'var(--quaternary-color, #2c5282)',
    titleColor: theme === 'light' ? 'var(--text-color, #333333)' : 'var(--text-color, #e7e9ef)',
    legendBackground: theme === 'light' ? 'transparent' : 'rgba(42, 48, 72, 0.6)',
    tooltipBackground: theme === 'light' ? 'var(--white-color, #ffffff)' : '#2a3048',
    tooltipBorder: theme === 'light' ? 'var(--gray-color, #e2e8f0)' : 'var(--quaternary-color, #2c5282)',
    tooltipText: theme === 'light' ? 'var(--text-color, #333333)' : 'var(--text-color, #e7e9ef)'
  };

  // Format values with appropriate units
  const formatWithUnits = (value) => {
    if (value >= 1000000) {
      return `${(value / 1000000).toFixed(1)}M`;
    } else if (value >= 1000) {
      return `${(value / 1000).toFixed(1)}K`;
    }
    return value.toFixed(0);
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

  // Use a reference to track in-flight requests and prevent race conditions
  const dataFetchInProgress = useRef(false);

  const fetchAllMetrics = async () => {
    // Don't start a new fetch if one is already in progress
    if (dataFetchInProgress.current) return;

    dataFetchInProgress.current = true;

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

      // Don't update state if component is unmounting or not visible
      if (!chartRef.current || !isVisible) {
        dataFetchInProgress.current = false;
        return;
      }

      dataTimestampRef.current = newDataTimestamp;

      // Use a single atomic update for state changes
      setMetricsData(prevData => {
        // More thorough comparison to prevent unnecessary updates
        const hasSignificantChanges = Object.keys(dataMap).some(path => {
          const prevValues = prevData[path]?.data?.map(d => d.value).join(',');
          const newValues = dataMap[path]?.data?.map(d => d.value).join(',');
          return prevValues !== newValues;
        });

        if (hasSignificantChanges) {
          // Trigger re-render atomically with the data change
          requestAnimationFrame(() => {
            setRenderTrigger(prev => prev + 1);
          });
          return dataMap;
        }
        return prevData;
      });
    } catch (error) {
      if (error.name !== 'AbortError') console.error(error);
    } finally {
      dataFetchInProgress.current = false;
    }
  };

  // Create a memoized draw chart function
  const drawChart = useCallback((dataMap) => {
    // Set drawing flag to prevent concurrent drawing operations
    if (isDrawingRef.current) {
      pendingDrawRef.current = true;
      return;
    }

    isDrawingRef.current = true;
    pendingDrawRef.current = false;

    const container = chartRef.current;
    if (!container) {
      isDrawingRef.current = false;
      return;
    }

    // Validate data
    const primaryData = dataMap[metricPaths[0]]?.data;
    if (!primaryData || !primaryData.length) {
      isDrawingRef.current = false;
      return;
    }

    // Clear any existing timeout
    if (drawTimeoutRef.current) {
      clearTimeout(drawTimeoutRef.current);
      drawTimeoutRef.current = null;
    }

    // IMPORTANT: Completely clean up existing SVG elements first
    d3.select(container).selectAll('svg').remove();

    // Dimensions - increased top margin to accommodate title and legend
    const margin = { top: 80, right: 30, bottom: 50, left: 60 };
    const width = container.clientWidth - margin.left - margin.right;
    const chartHeight = height - margin.top - margin.bottom;

    // Create SVG with theming
    // IMPORTANT: No initial opacity 0 or transitions to prevent flashing
    const svg = d3.select(container).append('svg')
      .attr('width', '100%')
      .attr('height', height)
      .style('shape-rendering', 'crispEdges')
      .style('background', themeColors.background) // Match container background
      .style('fill', 'none') // Ensure no fill is applied
      .style('border-radius', '8px'); // Match container border-radius

    svgRef.current = svg;

    // Add title at the top-left, above the chart area
    svg.append('text')
      .attr('x', margin.left)
      .attr('y', 20) // Fixed position for title
      .attr('class', theme === 'dark' ? 'dark-theme-title' : '')
      .style('fill', themeColors.titleColor)
      .style('font-size', '16px')
      .style('font-weight', '600')
      .style('dominant-baseline', 'middle')
      .text(title);

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

    // Apply theme-specific style to chart container and ensure consistent background
    d3.select(container)
      .classed('dark-theme-chart', theme === 'dark')
      .classed('light-theme-chart', theme === 'light')
      .style('background', themeColors.background);

    // Draw bars with theme adjustments
    g.selectAll('.layer')
      .data(stackedData)
      .enter().append('g')
      .attr('class', styles.layer)
      .attr('fill', d => {
        if (theme === 'dark') {
          const baseColor = d3.color(colorScale(d.key));
          if (baseColor) {
            baseColor.opacity = 0.8;
            return baseColor.brighter(0.3);
          }
        }
        return colorScale(d.key);
      })
      .selectAll('rect')
      .data(d => d)
      .enter().append('rect')
      .attr('x', d => xScale(d.data.date) - barWidth/2)
      .attr('width', barWidth)
      .attr('y', d => yScale(d[1]))
      .attr('height', d => yScale(d[0]) - yScale(d[1]))
      .attr('rx', 1)
      .attr('ry', 1)
      .style('shape-rendering', 'crispEdges');

    // Grid lines with theme styling
    g.append('g')
      .attr('class', `grid ${theme === 'dark' ? styles['dark-axis'] : ''}`)
      .call(
        d3.axisLeft(yScale)
          .ticks(5)
          .tickSize(-width)
          .tickFormat('')
      )
      .call(g => g.selectAll('.tick line')
        .style('stroke', themeColors.gridLines)
        .style('stroke-opacity', theme === 'dark' ? 0.3 : 0.5)
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
          .attr('class', theme === 'dark' ? styles['dark-total-label'] : styles.totalLabel)
          .style('fill', themeColors.text)
          .style('font-size', '10px')
          .text(formatWithUnits(total));
      }
    });

    // X-axis with theme styling
    g.append('g')
      .attr('class', theme === 'dark' ? styles['dark-axis'] : styles.axis)
      .attr('transform', `translate(0,${chartHeight})`)
      .call(d3.axisBottom(xScale)
        .ticks(5)
        .tickFormat(d => {
          const hours = d.getHours();
          const minutes = d.getMinutes();
          if (minutes === 0) {
            return `${hours % 12 || 12} ${hours < 12 ? 'AM' : 'PM'}`;
          } else {
            return `${hours.toString().padStart(2, '0')}:${minutes.toString().padStart(2, '0')}`;
          }
        }))
      .call(g => g.selectAll('.domain, .tick line')
        .style('stroke', themeColors.axis))
      .call(g => g.selectAll('.tick text')
        .style('fill', themeColors.timeAxisColor)
        .style('font-size', '10px'));

    // Y-axis with theme styling
    g.append('g')
      .attr('class', theme === 'dark' ? styles['dark-axis'] : styles.axis)
      .call(d3.axisLeft(yScale)
        .ticks(5)
        .tickFormat(formatWithUnits))
      .call(g => g.selectAll('.domain, .tick line')
        .style('stroke', themeColors.axis))
      .call(g => g.selectAll('.tick text')
        .style('fill', themeColors.axisText)
        .style('font-size', '10px'));

    // Legend with improved layout
    const legend = svg.append('g')
      .attr('transform', `translate(${margin.left}, 40)`) // Position below title
      .attr('class', theme === 'dark' ? styles['dark-legend'] : '');

    // Legend background for better contrast in dark mode
    if (theme === 'dark') {
      legend.append('rect')
        .attr('x', -5)
        .attr('y', -15)
        .attr('width', width + 10)
        .attr('height', 'auto') // Auto height based on content
        .attr('rx', 5)
        .attr('ry', 5)
        .attr('fill', themeColors.legendBackground)
        .attr('opacity', 0.6);
    }

    // Calculate legend layout with wrapping
    const legendItemHeight = 20;
    const legendItemPadding = 10;
    const maxLineWidth = width;
    let currentLineWidth = 0;
    let currentLine = 0;

    metricPaths.forEach((path, i) => {
      // Extract display name from path
      const pathLabel = path.split('.').pop();
      const textNode = legend.append('text')
        .attr('class', theme === 'dark' ? styles['dark-legend-text'] : styles.legendText)
        .style('fill', themeColors.text)
        .style('font-size', '12px')
        .text(pathLabel);

      const textWidth = textNode.node().getComputedTextLength() + 30; // Include space for color box

      // Check if we need to wrap to next line
      if (currentLineWidth + textWidth > maxLineWidth && currentLineWidth > 0) {
        currentLine++;
        currentLineWidth = 0;
      }

      // Create a group for this legend item
      const itemGroup = legend.append('g')
        .attr('transform', `translate(${currentLineWidth}, ${currentLine * legendItemHeight})`);

      // Add color box
      itemGroup.append('rect')
        .attr('width', 12)
        .attr('height', 12)
        .attr('y', 4)
        .attr('fill', () => {
          if (theme === 'dark') {
            const baseColor = d3.color(colorScale(path));
            if (baseColor) {
              baseColor.opacity = 0.8;
              return baseColor.brighter(0.3);
            }
          }
          return colorScale(path);
        });

      // Add text
      itemGroup.append('text')
        .attr('x', 16)
        .attr('y', 12)
        .attr('class', theme === 'dark' ? styles['dark-legend-text'] : styles.legendText)
        .style('fill', themeColors.text)
        .style('font-size', '12px')
        .text(pathLabel);

      // Remove the temporary text node used for measurement
      textNode.remove();

      // Update current line width
      currentLineWidth += textWidth;
    });

    // Adjust legend background height based on number of lines
    if (theme === 'dark') {
      legend.select('rect')
        .attr('height', (currentLine + 1) * legendItemHeight + 5);
    }

    // Reset drawing flag once complete
    setTimeout(() => {
      isDrawingRef.current = false;

      // If there's a pending draw request, process it
      if (pendingDrawRef.current && chartRef.current) {
        drawTimeoutRef.current = setTimeout(() => {
          drawChart(dataMap);
        }, 100);
      }
    }, 250); // Ensure transitions have time to complete
  }, [metricPaths, theme, height, themeColors]);

  // Setup data fetching
  useEffect(() => {
    // Setup fetch interval
    if (!isVisible || !metricPaths.length) return;

    // Cancel previous requests
    abortControllerRef.current.abort();
    abortControllerRef.current = new AbortController();

    // Initial fetch
    fetchAllMetrics();

    // Setup polling interval
    const intervalId = setInterval(fetchAllMetrics, 10000);

    return () => {
      clearInterval(intervalId);
      abortControllerRef.current.abort();
    };
  }, [metricPaths, context, isVisible]);

  // Unified chart drawing effect that handles all triggers
  useEffect(() => {
    if (!isVisible || !chartRef.current || Object.keys(metricsData).length === 0) {
      return;
    }

    // Clear any existing draw timeout
    if (drawTimeoutRef.current) {
      clearTimeout(drawTimeoutRef.current);
    }

    // Schedule drawing with a small delay to batch multiple updates
    drawTimeoutRef.current = setTimeout(() => {
      drawChart(metricsData);
    }, 50);

    return () => {
      if (drawTimeoutRef.current) {
        clearTimeout(drawTimeoutRef.current);
        drawTimeoutRef.current = null;
      }
    };
  }, [metricsData, renderTrigger, theme, isVisible, drawChart]);

  // Window resize handler with proper cleanup
  useEffect(() => {
    let resizeTimer;

    const handleResize = () => {
      if (!isVisible || !chartRef.current) return;

      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        // Increment render trigger to redraw on resize
        setRenderTrigger(prev => prev + 1);
      }, 250); // Longer debounce for resize events
    };

    window.addEventListener('resize', handleResize);

    return () => {
      window.removeEventListener('resize', handleResize);
      clearTimeout(resizeTimer);
    };
  }, [isVisible]);

  // IMPORTANT: Set the background color immediately on mount and theme changes
  useEffect(() => {
    if (chartRef.current) {
      chartRef.current.style.backgroundColor = themeColors.background;
    }
  }, [theme, themeColors.background]);

  return (
    <Box
      className={`${styles.container} ${className || ''} ${theme === 'dark' ? styles.darkContainer : ''}`}
      data-testid="chart-latch-tracing"
      style={{
        backgroundColor: themeColors.background
      }}
    >
      <div
        ref={chartRef}
        className={`${styles.chartContainer} ${theme === 'dark' ? styles.darkChartContainer : ''}`}
        style={{
          height: `${height}px`,
          position: 'relative',
          backgroundColor: themeColors.background, // Ensure consistent background
          borderRadius: '8px'
        }}
      />
    </Box>
  );
}

export default ChartLatchTracing;
