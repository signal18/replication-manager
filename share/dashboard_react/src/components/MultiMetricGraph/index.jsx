import React, { useEffect, useRef } from 'react';
import * as d3 from 'd3';

/**
 * A generic component for displaying multiple metrics in a single graph
 *
 * @param {Object} props
 * @param {Number} props.size - Width of the graph
 * @param {Number} props.step - Step size in milliseconds
 * @param {Object} props.context - Cubism context
 * @param {String} props.title - Main graph title
 * @param {Array} props.metrics - Array of metric objects
 * @param {String} props.metrics[].target - Graphite target for the metric
 * @param {String} props.metrics[].name - Display name for the metric
 * @param {String} props.metrics[].color - Color for the metric line (optional)
 * @param {Boolean} props.metrics[].dashed - Whether to use dashed line (optional)
 * @param {Boolean} props.metrics[].fill - Whether to fill area under line (optional)
 * @param {String} props.metrics[].fillColor - Fill color (optional)
 * @param {Boolean} props.metrics[].isMaxExtent - Whether this metric should be used as max extent (optional)
 * @param {String} props.maxExtent - Override for max extent (optional)
 * @param {String} props.className - CSS class name (optional)
 * @param {Object} props.clusterConfig - Cluster configuration with graphiteUrl
 */
function MultiMetricGraph({
  size,
  step,
  context,
  title,
  metrics,
  maxExtent,
  className,
  clusterConfig,
  showPercentage = false,
  percentageMetrics = [0, 1] // Default to comparing first two metrics
}) {
  const graphRef = useRef(null);

  useEffect(() => {
    if (!context || !graphRef.current || !metrics || metrics.length === 0) return;

    // Clear any existing content
    d3.select(graphRef.current).selectAll("*").remove();

    const width = size;
    const height = 120;
    const margin = { top: 20, right: 80, bottom: 30, left: 50 };

    const svg = d3.select(graphRef.current)
      .append("svg")
      .attr("width", width + margin.left + margin.right)
      .attr("height", height + margin.top + margin.bottom)
      .append("g")
      .attr("transform", `translate(${margin.left},${margin.top})`);

    // Create title
    svg.append("text")
      .attr("x", 0)
      .attr("y", -5)
      .attr("class", "title")
      .text(title);

    // Default colors if not specified
    const defaultColors = [
      "#1f77b4", // blue
      "#ff7f0e", // orange
      "#2ca02c", // green
      "#d62728", // red
      "#9467bd", // purple
      "#8c564b", // brown
      "#e377c2", // pink
      "#7f7f7f", // gray
      "#bcbd22", // olive
      "#17becf"  // teal
    ];

    // Prepare targets for the Graphite API
    const targets = metrics.map(m => m.target);
    const targetParams = targets.map(t => `target=${encodeURIComponent(t)}`).join('&');

    const endTime = Math.floor(Date.now() / 1000);
    const startTime = endTime - (size * step / 1000);

    // Format the URL for Graphite API
    const graphiteBaseUrl = clusterConfig?.graphiteUrl || 'http://your-graphite-url/render';
    const graphiteParams = `?${targetParams}&from=${startTime}&until=${endTime}&format=json`;
    const url = `${graphiteBaseUrl}${graphiteParams}`;

    // Fetch the data
    fetch(url)
      .then(response => response.json())
      .then(data => {
        if (!data || data.length === 0) return;

        // Process data for all metrics
        const processedData = data.map((metricData, index) => {
          const points = metricData.datapoints.filter(d => d[0] !== null);
          return {
            name: metrics[index].name,
            color: metrics[index].color || defaultColors[index % defaultColors.length],
            dashed: metrics[index].dashed || false,
            fill: metrics[index].fill || false,
            fillColor: metrics[index].fillColor || null,
            values: points.map(d => d[0]),
            times: points.map(d => new Date(d[1] * 1000)),
            isMaxExtent: metrics[index].isMaxExtent || false
          };
        });

        // Check if we have valid data
        if (processedData.some(d => d.values.length === 0)) return;

        // Use the times from the first metric as reference
        const timeData = processedData[0].times;

        // Determine y-axis max value
        let yMax = 0;

        // If a specific maxExtent is provided, use that
        if (maxExtent) {
          const maxExtentData = data.find(d => d.target === maxExtent);
          if (maxExtentData) {
            yMax = d3.max(maxExtentData.datapoints.map(d => d[0])) * 1.1;
          }
        }
        // If any metric is marked as maxExtent, use that
        else if (processedData.some(d => d.isMaxExtent)) {
          const maxExtentMetric = processedData.find(d => d.isMaxExtent);
          yMax = d3.max(maxExtentMetric.values) * 1.1;
        }
        // Otherwise use the max of all metrics
        else {
          yMax = d3.max(processedData, d => d3.max(d.values)) * 1.1;
        }

        // Create scales
        const xScale = d3.scaleTime()
          .domain(d3.extent(timeData))
          .range([0, width]);

        const yScale = d3.scaleLinear()
          .domain([0, yMax])
          .range([height, 0]);

        // Create axes
        const xAxis = d3.axisBottom(xScale)
          .ticks(5)
          .tickFormat(d3.timeFormat("%H:%M"));

        const yAxis = d3.axisLeft(yScale);

        // Add X axis
        svg.append("g")
          .attr("transform", `translate(0,${height})`)
          .call(xAxis);

        // Add Y axis
        svg.append("g")
          .call(yAxis);

        // Create and add lines for each metric
        processedData.forEach(metric => {
          // Create line generator
          const line = d3.line()
            .x((d, i) => xScale(metric.times[i]))
            .y(d => yScale(d));

          // Add line
          svg.append("path")
            .datum(metric.values)
            .attr("fill", "none")
            .attr("stroke", metric.color)
            .attr("stroke-width", 1.5)
            .attr("stroke-dasharray", metric.dashed ? "5,5" : "0")
            .attr("d", line);

          // Add fill if requested
          if (metric.fill) {
            svg.append("path")
              .datum(metric.values)
              .attr("fill", metric.fillColor || `${metric.color}33`) // Add transparency
              .attr("d", d3.area()
                .x((d, i) => xScale(metric.times[i]))
                .y0(height)
                .y1(d => yScale(d))
              );
          }
        });

        // Add percentage calculation if requested
        if (showPercentage && percentageMetrics.length >= 2) {
          const numericMetrics = percentageMetrics.map(idx => processedData[idx]).filter(Boolean);

          if (numericMetrics.length >= 2) {
            const numerator = numericMetrics[0].values[numericMetrics[0].values.length - 1];
            const denominator = numericMetrics[1].values[numericMetrics[1].values.length - 1];

            if (denominator !== 0) {
              const percentage = (numerator / denominator * 100).toFixed(1);

              svg.append("text")
                .attr("x", width - 120)
                .attr("y", 20)
                .attr("class", "percentage")
                .attr("text-anchor", "end")
                .text(`${numericMetrics[0].name}/${numericMetrics[1].name}: ${percentage}%`);
            }
          }
        }

        // Add legend
        const legend = svg.append("g")
          .attr("transform", `translate(${width + 5}, 0)`);

        processedData.forEach((metric, i) => {
          // Add color box
          legend.append("rect")
            .attr("x", 0)
            .attr("y", i * 25)
            .attr("width", 15)
            .attr("height", 15)
            .attr("fill", metric.color);

          // Add metric name
          legend.append("text")
            .attr("x", 20)
            .attr("y", i * 25 + 12)
            .text(metric.name);
        });
      })
      .catch(error => {
        console.error("Error fetching Graphite data:", error);
        svg.append("text")
          .attr("x", width / 2)
          .attr("y", height / 2)
          .attr("text-anchor", "middle")
          .text("Error loading data");
      });

  }, [context, size, step, title, metrics, maxExtent, clusterConfig, showPercentage, percentageMetrics]);

  // Apply inline styles to maintain component appearance without external CSS
  const graphStyle = {
    position: 'relative',
    marginBottom: '20px',
    border: '1px solid #ddd',
    borderRadius: '4px',
    background: '#fff',
    padding: '10px',
    boxShadow: '0 1px 3px rgba(0,0,0,0.1)'
  };

  const containerStyle = {
    width: '100%',
    height: '150px',
    overflow: 'hidden'
  };

  return (
    <div className={className} style={graphStyle}>
      <div ref={graphRef} style={containerStyle}></div>
    </div>
  );
}

export default MultiMetricGraph;
