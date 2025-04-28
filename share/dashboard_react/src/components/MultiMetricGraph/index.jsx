import React, { useEffect, useState, useRef } from 'react';
import { LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, Legend, ResponsiveContainer } from 'recharts';
import * as d3 from 'd3';
import moment from 'moment';

const MultiMetricGraph = ({ context, metrics = [], className }) => {
  const [data, setData] = useState([]);
  const [error, setError] = useState(null);
  const metricHandles = useRef([]);

  useEffect(() => {
    if (!context) return;

    try {
      const graphite = context.graphite('/graphite');
      metricHandles.current = metrics.map(metric => {
        // EXACT MATCH TO WORKING REQUEST FORMAT
        const target = `alias(${metric.target},'')`;

        const handle = context.metric((start, stop, step, callback) => {
          // Match the exact request format from working components
          const metricInstance = graphite.metric(target);

          metricInstance(start, stop, step, (data) => {
            console.log('Received data for', target, data);
            callback(data);
          });
        });

        return { ...metric, handle };
      });

      const updateData = () => {
        try {
          const now = Date.now();
          const step = context.step();
          const start = now - (context.size() * step);

          const timestamps = d3.range(start, now, step);
          const processedData = [];

          timestamps.forEach(timestamp => {
            const point = { timestamp, formattedTime: moment(timestamp).format('HH:mm:ss') };
            let hasData = false;

            metricHandles.current.forEach((metric, i) => {
              const value = metric.handle.valueAt(timestamp);
              if (value !== null && !isNaN(value)) {
                point[`metric${i}`] = value;
                hasData = true;
              }
            });

            if (hasData) processedData.push(point);
          });

          setData(processedData);
        } catch (err) {
          setError(err.message);
        }
      };

      context.on('change', updateData);
      updateData();

      return () => context.on('change', null);

    } catch (err) {
      setError(err.message);
    }
  }, [context, metrics]);

  // Debug UI and render logic
  if (error) return <div className={className} style={{ color: 'red' }}>{error}</div>;
  if (isLoading) return <div className={className}>Loading metrics...</div>;
  if (!data.length) return (
    <div className={className}>
      No metric data available
      <div style={{ fontSize: '0.8em', color: '#666' }}>
        Context: {context ? 'Valid' : 'Missing'} |
        Metrics: {metrics.length} |
        Last check: {new Date().toLocaleTimeString()}
      </div>
    </div>
  );

  return (
    <div className={className} ref={containerRef}>
      <ResponsiveContainer width="100%" height={400}>
        <LineChart
          data={data}
          margin={{ top: 20, right: 30, left: 20, bottom: 20 }}
        >
          <CartesianGrid strokeDasharray="3 3" stroke="#eee" />
          <XAxis
            dataKey="formattedTime"
            tick={{ fontSize: 12 }}
            label={{
              value: 'Time',
              position: 'insideBottomRight',
              offset: 0
            }}
          />
          <YAxis
            tick={{ fontSize: 12 }}
            label={{
              value: 'Value',
              angle: -90,
              position: 'insideLeft',
              offset: 10
            }}
          />
          <Tooltip
            contentStyle={{
              background: '#fff',
              border: '1px solid #ddd',
              borderRadius: 4
            }}
            labelFormatter={(label) => (
              <div style={{ fontWeight: 'bold' }}>
                {moment(label).format('HH:mm:ss')}
              </div>
            )}
            formatter={(value, name) => [
              Number(value).toLocaleString(),
              data[0][`metricName${name.replace('metric', '')}`] || name
            ]}
          />
          <Legend
            wrapperStyle={{ paddingTop: 10 }}
            formatter={(value, entry) => (
              <span style={{ color: entry.color }}>
                {entry.payload?.metricName || value}
              </span>
            )}
          />
          {metricHandles.map((metric, index) => (
            <Line
              key={index}
              type="monotone"
              dataKey={`metric${index}`}
              name={metric.name}
              stroke={metric.color || '#8884d8'}
              strokeWidth={2}
              dot={false}
              activeDot={{
                r: 6,
                fill: '#fff',
                strokeWidth: 2
              }}
              isAnimationActive={false}
            />
          ))}
        </LineChart>
      </ResponsiveContainer>

      {process.env.NODE_ENV === 'development' && renderDebugInfo()}
    </div>
  );
};

export default MultiMetricGraph;
