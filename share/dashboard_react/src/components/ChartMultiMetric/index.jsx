import React, { useEffect, useRef, useState, useCallback } from 'react';
import { Box } from '@chakra-ui/react';
import * as d3 from 'd3';
import styles from '../../styles/_chartmultimetric.module.scss';
import { useTheme } from '../../ThemeProvider';

function ChartMultiMetric({
  context,
  className,
  maxExtent,
  metricPaths = [],
  title = "Metrics",
  height = 300,
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

  const getDisplayName = (metricPath) => {
    const parts = metricPath.split('.');
    let displayName = parts[parts.length - 1] || metricPath;

    // Remove first 3 underscore-separated words
    const nameParts = displayName.split('_');
    displayName = nameParts.slice(3).join('_');

    // Remove trailing closing parenthesis
    displayName = displayName.replace(/\)+$/, '');

    return displayName || metricPath; // Fallback to original if empty
  };

  // Helper function to format numbers with units (K, M, G, T)
  const formatWithUnits = (value) => {
    if (value === 0) return '0';

    const k = 1024;
    const sizes = ['', 'K', 'M', 'G', 'T', 'P', 'E'];

    // Find the right unit
    const i = Math.floor(Math.log(Math.abs(value)) / Math.log(k));

    // Don't go beyond our available units
    const unitIndex = Math.min(i, sizes.length - 1);

    // Format with the appropriate unit
    if (unitIndex === 0) {
      return d3.format(',.1f')(value);
    } else {
      return d3.format(',.1f')(value / Math.pow(k, unitIndex)) + sizes[unitIndex];
    }
  };

  // Function to directly fetch data from the Graphite API
  const fetchMetricData = async (metricPath) => {
    try {
      const now = Math.floor(Date.now() / 1000);
      const step = context.step() / 1000; // Convert to seconds
      const size = context.size();
      const from = now - (size * step);
      const until = now;

      // Encode the metric path for the URL
      const encodedTarget = encodeURIComponent(`alias(${metricPath},'')`);

      // Create the API URL similar to your working examples
      const url = `/graphite/render?format=raw&target=${encodedTarget}&from=${from}&until=${until}`;

      const response = await fetch(url, {
        signal: abortControllerRef.current.signal
      });

      if (!response.ok) {
        throw new Error(`HTTP error! Status: ${response.status}`);
      }

      const text = await response.text();

      // Parse the response - format is expected to be something like:
      // ,startTime,endTime,step|value1,value2,value3,...
      const parts = text.split('|');
      if (parts.length !== 2) {
        console.error(`Unexpected response format for ${metricPath}`);
        return null;
      }

      const timeInfo = parts[0].split(',');
      if (timeInfo.length !== 4) {
        console.error(`Unexpected time format for ${metricPath}`);
        return null;
      }

      const startTime = parseInt(timeInfo[1]) * 1000; // Convert to ms
      const endTime = parseInt(timeInfo[2]) * 1000;   // Convert to ms
      const stepTime = parseInt(timeInfo[3]) * 1000;  // Convert to ms

      const values = parts[1].split(',');

      // Create data points
      const data = values.map((value, i) => {
        const val = value === 'None' ? 0 : parseFloat(value) || 0;
        return {
          date: new Date(startTime + (i * stepTime)),
          value: val
        };
      }).filter(d => !isNaN(d.value));

      return {
        path: metricPath,
        displayName: getDisplayName(metricPath),
        data
      };
    } catch (error) {
      if (error.name !== 'AbortError') {
        console.error(`Error fetching data for ${metricPath}:`, error);
      }
      return {
        path: metricPath,
        displayName: getDisplayName(metricPath),
        data: []
      };
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
        if (curr) acc[curr.path] = curr;
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
    const allData = Object.values(dataMap).flatMap(item => item.data);
    if (!allData.length) {
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

    const startTime = d3.min(allData, d => d.date);
    const endTime = d3.max(allData, d => d.date);

    // Scales
    const xScale = d3.scaleTime()
      .domain([startTime, endTime])
      .range([0, width])
      .nice();

    const maxValue = maxExtent !== undefined ? maxExtent : d3.max(allData, d => d.value) * 1.1 || 1;
    const yScale = d3.scaleLinear()
      .domain([0, maxValue])
      .nice()
      .range([chartHeight, 0]);

    // Apply theme-specific style to chart container and ensure consistent background
    d3.select(container)
      .classed('dark-theme-chart', theme === 'dark')
      .classed('light-theme-chart', theme === 'light')
      .style('background', themeColors.background);

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

    // Create the line generator
    const line = d3.line()
      .defined(d => !isNaN(d.value))
      .x(d => xScale(d.date))
      .y(d => yScale(d.value))
      .curve(d3.curveMonotoneX);

    // Draw lines with theme-adjusted colors
    Object.values(dataMap).forEach((item) => {
      if (!item.data.length) return;

      let lineColor = colorScale(item.path);
      if (theme === 'dark') {
        const baseColor = d3.color(lineColor);
        if (baseColor) {
          baseColor.opacity = 0.8;
          lineColor = baseColor.brighter(0.3);
        }
      }

      g.append('path')
        .datum(item.data)
        .attr('class', styles.line)
        .attr('d', line)
        .style('stroke', lineColor)
        .style('fill', 'none')
        .style('stroke-width', 1.5);
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
      .attr('transform', `translate(${Math.floor(margin.left)},${40})`)
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

    Object.values(dataMap).forEach((item) => {
      // Extract display name from path
      const textNode = legend.append('text')
        .attr('class', theme === 'dark' ? styles['dark-legend-text'] : styles.legendText)
        .style('fill', themeColors.text)
        .style('font-size', '12px')
        .text(item.displayName);

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
            const baseColor = d3.color(colorScale(item.path));
            if (baseColor) {
              baseColor.opacity = 0.8;
              return baseColor.brighter(0.3);
            }
          }
          return colorScale(item.path);
        });

      // Add text
      itemGroup.append('text')
        .attr('x', 16)
        .attr('y', 12)
        .attr('class', theme === 'dark' ? styles['dark-legend-text'] : styles.legendText)
        .style('fill', themeColors.text)
        .style('font-size', '12px')
        .text(item.displayName);

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

    // Add tooltip with theme support
    const tooltip = d3.select(container).append('div')
      .attr('class', theme === 'dark' ? styles.darkTooltip : styles.tooltip)
      .style('opacity', 0)
      .style('background', themeColors.tooltipBackground)
      .style('color', themeColors.tooltipText)
      .style('border', `1px solid ${themeColors.tooltipBorder}`);

    // Add rule line
    const rule = g.append('line')
      .attr('class', styles.rule)
      .attr('y1', 0)
      .attr('y2', chartHeight)
      .style('stroke', themeColors.text)
      .style('stroke-width', '1px')
      .style('opacity', 0);

    // Mouse interaction area
    g.append('rect')
      .attr('width', width)
      .attr('height', chartHeight)
      .style('opacity', 0)
      .on('mousemove', (event) => {
        const [xPos] = d3.pointer(event);
        const date = xScale.invert(xPos);

        rule.style('opacity', 1)
          .attr('x1', xPos)
          .attr('x2', xPos);

        // Update tooltip
        const tooltipContent = Object.values(dataMap).map(item => {
          // Find closest data point
          const bisect = d3.bisector(d => d.date).left;
          const i = bisect(item.data, date, 1);
          const d0 = item.data[i - 1];
          const d1 = item.data[i];
          const d = d0 && d1 ?
            (date - d0.date > d1.date - date ? d1 : d0) :
            (d0 || d1);

          let itemColor = colorScale(item.path);
          if (theme === 'dark') {
            const baseColor = d3.color(itemColor);
            if (baseColor) {
              baseColor.opacity = 0.8;
              itemColor = baseColor.brighter(0.3).formatHex();
            }
          }

          return `
            <div class="${theme === 'dark' ? styles.darkTooltipItem : styles.tooltipItem}">
              <span class="${styles.tooltipColor}"
                    style="background:${itemColor}"></span>
              <span class="${theme === 'dark' ? styles.darkTooltipLabel : styles.tooltipLabel}">${item.displayName}:</span>
              <span class="${theme === 'dark' ? styles.darkTooltipValue : styles.tooltipValue}">${d ? formatWithUnits(d.value) : 'N/A'}</span>
            </div>
          `;
        }).join('');

        tooltip
          .html(tooltipContent)
          .style('left', `${event.pageX + 10}px`)
          .style('top', `${event.pageY - 10}px`)
          .style('opacity', 1);
      })
      .on('mouseout', () => {
        rule.style('opacity', 0);
        tooltip.style('opacity', 0);
      });

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
  }, [metricPaths, theme, height, themeColors, maxExtent]);

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
      data-testid="chart-multi-metric"
      style={{
        backgroundColor: themeColors.background
      }}
    >
    <div
  ref={chartRef}
  className={`${styles.chartContainer} ${theme === 'dark' ? styles.darkChartContainer : ''}`}
  style={{
    height: `${height}px`,
    position: 'relative',  // Ensure this is relative
    overflow: 'hidden',    // Prevent any overflow from affecting layout
    backgroundColor: themeColors.background,
    borderRadius: '8px'
  }}
      />
    </Box>
  );
}

export default ChartMultiMetric;
