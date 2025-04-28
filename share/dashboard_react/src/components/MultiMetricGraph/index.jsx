import React, { useState, useEffect } from 'react';
import { LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, Legend, ResponsiveContainer } from 'recharts';
import axios from 'axios'; // Make sure axios is installed or use fetch instead

const MultiMetricGraph = ({ metrics, timeRange, ...props }) => {
  const [chartData, setChartData] = useState([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState(null);

  useEffect(() => {
    const fetchGraphiteData = async () => {
      try {
        setIsLoading(true);
        setError(null);

        if (!metrics || !Array.isArray(metrics) || metrics.length === 0) {
          console.log("No metrics provided");
          setChartData([]);
          setIsLoading(false);
          return;
        }

        console.log("Metrics to fetch:", metrics);

        // Determine time range parameters
        const now = Math.floor(Date.now() / 1000);
        const from = timeRange?.from || (now - 3600); // Default to last hour
        const until = timeRange?.until || now;

        // Create an array of promises for each metric
        const promises = metrics.map(async (metric) => {
          if (!metric.target) {
            console.warn("Metric missing target:", metric);
            return null;
          }

          // Build the correct Graphite API URL
          // Note: Using /render instead of /api/graphite/render based on your log
          const encodedTarget = encodeURIComponent(metric.target);
          const url = `/render?target=${encodedTarget}&format=json&from=${from}&until=${until}`;

          console.log(`Fetching data for ${metric.name || metric.target} from ${url}`);

          try {
            const response = await axios.get(url);

            if (response.data && Array.isArray(response.data) && response.data.length > 0) {
              // Assign name and color from our metric object
              const data = response.data[0];
              data.name = metric.name || data.target;
              data.color = metric.color || '#000000';
              return data;
            } else {
              console.warn(`No data returned for ${metric.target}`);
              return null;
            }
          } catch (err) {
            console.error(`Error fetching data for ${metric.target}:`, err);
            return null;
          }
        });

        // Wait for all requests to complete
        const results = await Promise.all(promises);
        const validResults = results.filter(r => r !== null && r.datapoints && r.datapoints.length > 0);

        console.log("Fetched metrics data:", validResults);

        if (validResults.length === 0) {
          console.warn("No valid metric data fetched");
          setChartData([]);
          setIsLoading(false);
          return;
        }

        // Extract all timestamps
        const timestamps = new Set();
        validResults.forEach(metric => {
          if (metric.datapoints) {
            metric.datapoints.forEach(point => {
              if (Array.isArray(point) && point.length > 1 && point[1] !== null) {
                timestamps.add(point[1]);
              }
            });
          }
        });

        // Convert to array and sort
        const sortedTimestamps = Array.from(timestamps).sort((a, b) => a - b);

        // Create data points for each timestamp
        const data = sortedTimestamps.map(timestamp => {
          const date = new Date(timestamp * 1000);
          const formattedTime = date.toTimeString().split(' ')[0];

          const dataPoint = {
            timestamp,
            time: formattedTime
          };

          // Add values for each metric at this timestamp
          validResults.forEach((metric, index) => {
            const point = metric.datapoints.find(dp => dp[1] === timestamp);
            const value = point ? point[0] : null;

            if (value !== null && !isNaN(value)) {
              dataPoint[`metric${index}`] = Number(value);
              dataPoint[`name${index}`] = metric.name || metric.target;
              dataPoint[`color${index}`] = metric.color || getColorForIndex(index);
            }
          });

          return dataPoint;
        });

        console.log(`Generated ${data.length} chart data points`);
        setChartData(data);
      } catch (err) {
        console.error("Error in fetchGraphiteData:", err);
        setError(err.message || "Failed to fetch metric data");
      } finally {
        setIsLoading(false);
      }
    };

    fetchGraphiteData();
  }, [metrics, timeRange]);

  if (isLoading) return <div>Loading metric data...</div>;
  if (error) return <div>Error: {error}</div>;
  if (!chartData || chartData.length === 0) {
    return (
      <div>
        <div>No data available</div>
        <div style={{ marginTop: '10px', fontSize: '0.9em', color: '#666' }}>
          Check browser console for detailed error information.
        </div>
      </div>
    );
  }

  return (
    <div className="multi-metric-graph" {...props}>
      <ResponsiveContainer width="100%" height={400}>
        <LineChart
          data={chartData}
          margin={{ top: 5, right: 30, left: 20, bottom: 5 }}
        >
          <CartesianGrid strokeDasharray="3 3" />
          <XAxis
            dataKey="time"
            label={{ value: 'Time', position: 'insideBottomRight', offset: 0 }}
            animationDuration={0}
          />
          <YAxis
            label={{ value: 'Value', angle: -90, position: 'insideLeft' }}
            animationDuration={0}
          />
          <Tooltip
            formatter={(value, name, props) => {
              const metricIndex = name.replace('metric', '');
              const metricName = props.payload[`name${metricIndex}`] || name;
              return [value, metricName];
            }}
            labelFormatter={(label) => `Time: ${label}`}
            isAnimationActive={false}
          />
          <Legend />
          {chartData.length > 0 && metrics.map((metric, index) => {
            // Check if we have data for this metric
            const hasData = Object.keys(chartData[0]).includes(`metric${index}`);
            if (!hasData) return null;

            return (
              <Line
                key={index}
                type="monotone"
                dataKey={`metric${index}`}
                name={metric.name || metric.target || `Metric ${index + 1}`}
                stroke={metric.color || getColorForIndex(index)}
                dot={false}
                activeDot={{ r: 6 }}
                isAnimationActive={false}
              />
            );
          })}
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
};

// Helper function to get colors for lines
const getColorForIndex = (index) => {
  const colors = ['#8884d8', '#82ca9d', '#ffc658', '#ff8042', '#0088FE', '#00C49F'];
  return colors[index % colors.length];
};

export default MultiMetricGraph;
