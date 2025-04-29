import React, { useEffect, useRef } from 'react';
import { Box } from '@chakra-ui/react';
import * as d3 from 'd3';
import styles from '../../styles/_multimetricgraph.module.scss';

function MultiMetricGraph({
  context,
  className,
  maxExtent,
  metricPaths = [],
  title = "Metrics",
  height = 300
}) {
  const chartRef = useRef(null);
  const colorScale = d3.scaleOrdinal(d3.schemeCategory10);

  const getDisplayName = (metricPath) => {
    const parts = metricPath.split('.');
    return parts[parts.length - 1];
  };

  useEffect(() => {
    if (!chartRef.current || !context || !metricPaths.length) return;

    // Clear previous chart
    const container = d3.select(chartRef.current);
    container.selectAll('*').remove();

    // Get context time parameters
    const now = Date.now();
    const startTime = now - (context.size() * context.step());
    const endTime = now;
    const step = context.step();

    // Create SVG elements
    const svg = container.append('svg')
      .attr('width', '100%')
      .attr('height', height)
      .attr('class', styles.chartSvg);

    // Set up dimensions
    const margin = { top: 20, right: 30, bottom: 30, left: 40 };
    const chartWidth = chartRef.current.clientWidth - margin.left - margin.right;
    const chartHeight = height - margin.top - margin.bottom;

    // Create scales
    const xScale = d3.scaleTime()
      .domain([new Date(startTime), new Date(endTime)])
      .range([0, chartWidth]);

    const yScale = d3.scaleLinear()
      .range([chartHeight, 0]);

    // Create line generator
    const line = d3.line()
      .defined(d => !isNaN(d.value))
      .x(d => xScale(d.date))
      .y(d => yScale(d.value));

    // Connect to graphite
    const graphite = context.graphite('/graphite');

    // Fetch data - FIXED SECTION
    // Looking at the successful horizon chart approach
    // This is based on how the working MutexTracingGraph component handles data
    const metrics = metricPaths.map(path => {
      try {
        const metric = graphite.metric(path);
        return metric.alias ? metric.alias(getDisplayName(path)) : metric;
      } catch (error) {
        console.error(`Error creating metric for ${path}:`, error);
        return null;
      }
    }).filter(Boolean); // Remove any null metrics

    // Create points for the chart
    const size = context.size();
    const values = {};

    // First setup data structure for each metric
    metricPaths.forEach(path => {
      values[path] = {
        path,
        data: []
      };
    });

    // Extract data if possible - similar to horizon chart approach
    const fetchData = () => {
      for (let i = 0; i < metrics.length; i++) {
        const metric = metrics[i];
        const path = metricPaths[i];

        if (!metric || !path) continue;

        // Try to access the data as a property or method
        try {
          // Reset data array
          values[path].data = [];

          // Try multiple methods to get the data based on cubism API
          const data = typeof metric.toString === 'function' ?
            metric.toString() :
            (Array.isArray(metric) ? metric : []);

          // Add data points
          for (let j = 0; j < size; j++) {
            const value = parseFloat(data[j]);
            if (!isNaN(value)) {
              values[path].data.push({
                date: new Date(startTime + (j * step)),
                value: value
              });
            }
          }
        } catch (error) {
          console.error(`Failed to extract data for ${path}:`, error);
        }
      }

      return Object.values(values);
    };

    // Use a Promise to handle the data fetching
    const fetchPromise = new Promise(resolve => {
      // If the context has a 'on' method for listening to updates
      if (typeof context.on === 'function') {
        context.on('prepare', () => {
          resolve(fetchData());
        });

        // Set a timeout in case the 'prepare' event doesn't fire
        setTimeout(() => {
          resolve(fetchData());
        }, 500);
      } else {
        // Just resolve with current data
        resolve(fetchData());
      }
    });

    // Wait for data to be fetched
    fetchPromise.then(results => {
      // Set Y domain
      const allValues = results.flatMap(r => r.data.map(d => d.value));
      yScale.domain(maxExtent !== undefined ?
        [0, maxExtent] :
        [0, d3.max(allValues) || 1]  // Added fallback if there's no data
      ).nice();

      // Create chart group
      const g = svg.append('g')
        .attr('transform', `translate(${margin.left},${margin.top})`);

      // Draw lines
      results.forEach(({ path, data }) => {
        g.append('path')
          .datum(data)
          .attr('class', styles.line)
          .attr('d', line)
          .style('stroke', colorScale(path));
      });

      // Add X axis
      g.append('g')
        .attr('transform', `translate(0,${chartHeight})`)
        .call(d3.axisBottom(xScale).ticks(5))
        .attr('class', styles.axis);

      // Add Y axis
      g.append('g')
        .call(d3.axisLeft(yScale))
        .attr('class', styles.axis);

      // Add legend
      const legend = g.append('g')
        .attr('transform', `translate(${chartWidth - 120}, 0)`);

      metricPaths.forEach((path, i) => {
        legend.append('rect')
          .attr('x', 0)
          .attr('y', i * 20)
          .attr('width', 15)
          .attr('height', 15)
          .style('fill', colorScale(path));

        legend.append('text')
          .attr('x', 20)
          .attr('y', i * 20 + 12)
          .text(getDisplayName(path))
          .attr('class', styles.legendText);
      });

      // Add tooltip
      const tooltip = container.append('div')
        .attr('class', styles.tooltip);

      // Mouse interaction
      g.append('rect')
        .attr('width', chartWidth)
        .attr('height', chartHeight)
        .style('opacity', 0)
        .on('mousemove', (event) => {
          const [xPos] = d3.pointer(event);
          const date = xScale.invert(xPos);

          const tooltipContent = results.map(({ path, data }) => {
            const bisect = d3.bisector(d => d.date).left;
            const i = bisect(data, date, 1);
            const d0 = data[i - 1];
            const d1 = data[i];
            const d = date - d0?.date > d1?.date - date ? d1 : d0;
            return `
              <div class="${styles.tooltipItem}">
                <span class="${styles.tooltipColor}"
                      style="background:${colorScale(path)}"></span>
                ${getDisplayName(path)}: ${d?.value?.toFixed(2) || 'N/A'}
              </div>
            `;
          }).join('');

          tooltip
            .html(tooltipContent)
            .style('left', `${event.pageX + 10}px`)
            .style('top', `${event.pageY - 10}px`)
            .style('opacity', 1); // Make sure tooltip is visible
        })
        .on('mouseout', () => tooltip.style('opacity', 0));

      // Add rule similar to the horizon chart for better interaction
      const rule = g.append('line')
        .attr('class', styles.rule)
        .attr('y1', 0)
        .attr('y2', chartHeight)
        .style('stroke', '#000')
        .style('stroke-width', '1px')
        .style('opacity', 0);

      // Handle rule positioning for mouseover
      context.on('focus', function(i) {
        if (i == null) {
          rule.style('opacity', 0);
          tooltip.style('opacity', 0);
        } else {
          rule.style('opacity', 1)
            .attr('x1', xScale(new Date(startTime + i * step)))
            .attr('x2', xScale(new Date(startTime + i * step)));
        }
      });

    }).catch(console.error);

    // Handle resize
    const resizeObserver = new ResizeObserver(() => {
      // Just trigger a re-render by simply removing all elements
      container.selectAll('*').remove();
    });

    resizeObserver.observe(chartRef.current);

    return () => {
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

export default MultiMetricGraph;
