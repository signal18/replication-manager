import React, { useEffect, useRef, useState } from 'react';
import { Box } from '@chakra-ui/react';
import * as d3 from 'd3';
import styles from '../../styles/_chartmultimetric.module.scss';

function ChartMultiMetric({
  context,
  className,
  maxExtent,
  metricPaths = [],
  title = "Metrics",
  height = 300
}) {
  const chartRef = useRef(null);
  const colorScale = d3.scaleOrdinal(d3.schemeCategory10);
  const [metricsData, setMetricsData] = useState({});

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

      console.log(`Fetching data from: ${url}`);

      const response = await fetch(url);
      if (!response.ok) {
        throw new Error(`HTTP error! Status: ${response.status}`);
      }

      const text = await response.text();
      console.log(`Response for ${metricPath}:`, text);

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
        return {
          date: new Date(startTime + (i * stepTime)),
          value: parseFloat(value)
        };
      }).filter(d => !isNaN(d.value));

      return {
        path: metricPath,
        displayName: getDisplayName(metricPath),
        data
      };
    } catch (error) {
      console.error(`Error fetching data for ${metricPath}:`, error);
      return {
        path: metricPath,
        displayName: getDisplayName(metricPath),
        data: []
      };
    }
  };

  useEffect(() => {
    if (!chartRef.current || !context || !metricPaths.length) return;

    console.log("MultiMetricGraph initializing with metrics:", metricPaths);

    // Clear previous chart
    const container = d3.select(chartRef.current);
    container.selectAll('*').remove();

    // Create the graphite connection for context metrics
    const graphite = context.graphite('/graphite');
    console.log("Graphite connection established");

    // Create metric objects for context
    const metrics = metricPaths.map(path => {
      console.log(`Creating metric for path: ${path}`);
      const displayName = getDisplayName(path);
      return graphite.metric(path).alias(displayName);
    });

    // Add metrics to context
    metrics.forEach(metric => {
      if (!context.metrics) {
        context.metrics = [];
      }
      if (!context.metrics.includes(metric)) {
        context.metrics.push(metric);
      }
    });

    // Manually fetch data for all metrics
    const fetchAllData = async () => {
      const results = {};

      for (const path of metricPaths) {
        const result = await fetchMetricData(path);
        if (result) {
          results[path] = result;
        }
      }

      setMetricsData(results);
      renderChart(results);
    };

    // Call fetch function
    fetchAllData();

    // Function to render the chart with fetched data
    const renderChart = (dataMap) => {
      if (Object.keys(dataMap).length === 0) return;

      const allData = Object.values(dataMap).flatMap(item => item.data);
      const startTime = allData.length ? d3.min(allData, d => d.date) : Date.now() - (context.size() * context.step());
      const endTime = allData.length ? d3.max(allData, d => d.date) : Date.now();

      const margin = { top: 40, right: 30, bottom: 30, left: 60 };
      const chartWidth = chartRef.current.clientWidth - margin.left - margin.right;
      const chartHeight = height - margin.top - margin.bottom;

      const svg = container.append('svg')
        .attr('width', '100%')
        .attr('height', height)
        .attr('class', styles.chartSvg);

      const g = svg.append('g')
        .attr('transform', `translate(${margin.left},${margin.top})`);

      const xScale = d3.scaleTime()
        .domain([new Date(startTime), new Date(endTime)])
        .range([0, chartWidth]);

      const maxValue = maxExtent !== undefined ? maxExtent : d3.max(allData, d => d.value) * 1.1 || 1;
      const yScale = d3.scaleLinear()
        .domain([0, maxValue])
        .range([chartHeight, 0])
        .nice();

      const line = d3.line()
        .defined(d => !isNaN(d.value))
        .x(d => xScale(d.date))
        .y(d => yScale(d.value))
        .curve(d3.curveMonotoneX);

      // Add X axis
      g.append('g')
        .attr('transform', `translate(0,${chartHeight})`)
        .attr('class', styles.axis)
        .call(d3.axisBottom(xScale).ticks(5));

      // Add Y axis
      g.append('g')
        .attr('class', styles.axis)
        .call(d3.axisLeft(yScale).ticks(5).tickFormat(formatWithUnits));

      // Draw lines
      Object.values(dataMap).forEach((item) => {
        if (!item.data.length) return;
        g.append('path')
          .datum(item.data)
          .attr('class', styles.line)
          .attr('d', line)
          .style('stroke', colorScale(item.path))
          .style('fill', 'none')
          .style('stroke-width', 1.5);
      });

      // Top-left legend
      const legend = g.append('g')
        .attr('transform', `translate(0,-20)`);

      let xOffset = 0;
      Object.values(dataMap).forEach((item) => {
        const legendGroup = legend.append('g')
          .attr('transform', `translate(${xOffset},0)`);

        legendGroup.append('rect')
          .attr('width', 12)
          .attr('height', 12)
          .attr('y', 2)
          .style('fill', colorScale(item.path));

        legendGroup.append('text')
          .attr('x', 18)
          .attr('y', 12)
          .text(item.displayName)
          .attr('class', styles.legendText);

        xOffset += 12 + 18 + (item.displayName.length * 8); // Approximate text width
      });

      // Add tooltip
      const tooltip = container.append('div')
        .attr('class', styles.tooltip)
        .style('opacity', 0);

      // Add rule line
      const rule = g.append('line')
        .attr('class', styles.rule)
        .attr('y1', 0)
        .attr('y2', chartHeight)
        .style('stroke', '#000')
        .style('stroke-width', '1px')
        .style('opacity', 0);

      // Mouse interaction area
      g.append('rect')
        .attr('width', chartWidth)
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

            return `
              <div class="${styles.tooltipItem}">
                <span class="${styles.tooltipColor}"
                      style="background:${colorScale(item.path)}"></span>
                <span class="${styles.tooltipLabel}">${item.displayName}:</span>
                <span class="${styles.tooltipValue}">${d ? formatWithUnits(d.value) : 'N/A'}</span>
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

      // Add Y-axis label
      g.append('text')
        .attr('transform', 'rotate(-90)')
        .attr('y', -margin.left + 20)
        .attr('x', -chartHeight / 2)
        .attr('text-anchor', 'middle')
        .attr('class', styles.axisLabel)
        .text('Value');
    };

    // Set up focus for context
    context.on('focus', (i) => {
      if (i == null) return;
      // This keeps the context in sync but our chart uses direct API data
    });

    // Initial focus to trigger data loading in context
    context.focus(context.size() / 2);

    // Handle resize
    const resizeObserver = new ResizeObserver(() => {
      container.selectAll('*').remove();
      if (Object.keys(metricsData).length > 0) {
        renderChart(metricsData);
      }
    });

    resizeObserver.observe(chartRef.current);

    return () => {
      console.log("MultiMetricGraph unmounting");
      resizeObserver.disconnect();
      context.on('focus', null); // Clear focus handler
      container.selectAll('*').remove();
    };
  }, [context, metricPaths, height, maxExtent, colorScale]);

  return (
    <div className={styles.container}>
      {title && <h3 className={styles.title}>{title}</h3>}
      <Box
        ref={chartRef}
        className={`${styles.chartContainer} ${className || ''}`}
        width="100%"
        height={height}
      />
    </div>
  );
}

export default ChartMultiMetric;
