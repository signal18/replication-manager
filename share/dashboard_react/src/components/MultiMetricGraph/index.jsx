import React, { useEffect, useRef } from 'react';
import { Box } from '@chakra-ui/react';
import * as d3 from 'd3';
import styles from '../../styles/_multimetricgraph.module.scss'; // Updated CSS Modules import

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
      .attr('class', styles.chartSvg); // Use CSS Module class

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

    // Fetch data
    Promise.all(
      metricPaths.map(path => new Promise((resolve) => {
        const metric = graphite.metric(path).alias(getDisplayName(path));
        metric(startTime, endTime, step, (error, data) => {
          resolve({
            path,
            data: data.map(d => ({
              date: new Date(d.date),
              value: +d.value
            }))
          });
        });
      }))
    ).then(results => {
      // Set Y domain
      const allValues = results.flatMap(r => r.data.map(d => d.value));
      yScale.domain(maxExtent !== undefined ?
        [0, maxExtent] :
        [0, d3.max(allValues)]
      ).nice();

      // Create chart group
      const g = svg.append('g')
        .attr('transform', `translate(${margin.left},${margin.top})`);

      // Draw lines
      results.forEach(({ path, data }) => {
        g.append('path')
          .datum(data)
          .attr('class', styles.line) // CSS Module class
          .attr('d', line)
          .style('stroke', colorScale(path));
      });

      // Add X axis
      g.append('g')
        .attr('transform', `translate(0,${chartHeight})`)
        .call(d3.axisBottom(xScale).ticks(5))
        .attr('class', styles.axis); // CSS Module class

      // Add Y axis
      g.append('g')
        .call(d3.axisLeft(yScale))
        .attr('class', styles.axis); // CSS Module class

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
          .attr('class', styles.legendText); // CSS Module class
      });

      // Add tooltip
      const tooltip = container.append('div')
        .attr('class', styles.tooltip); // CSS Module class

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
                ${getDisplayName(path)}: ${d?.value.toFixed(2) || 'N/A'}
              </div>
            `;
          }).join('');

          tooltip
            .html(tooltipContent)
            .style('left', `${event.pageX + 10}px`)
            .style('top', `${event.pageY - 10}px`);
        })
        .on('mouseout', () => tooltip.style('opacity', 0));

    }).catch(console.error);

    // Handle resize
    const resizeObserver = new ResizeObserver(() => {
      container.selectAll('*').remove();
      // Recreate the chart on resize
      useEffect(() => {}, [context, metricPaths, height, maxExtent]);
    });

    resizeObserver.observe(chartRef.current);

    return () => {
      resizeObserver.disconnect();
      container.selectAll('*').remove();
    };
  }, [context, metricPaths, height, maxExtent]);

  return (
    <div className={styles.container}>
      {title && <h3 className={styles.title}>{title}</h3>}
      <Box
        ref={chartRef}
        className={`${styles.chartContainer} ${className}`}
        width="100%"
        height={height}
      />
    </div>
  );
}

export default MultiMetricGraph;
