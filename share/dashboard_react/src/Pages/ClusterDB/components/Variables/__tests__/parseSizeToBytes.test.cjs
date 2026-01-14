// BigInt Size Parsing Test Suite
// Tests the JavaScript parseSizeToBytes function from Variables component

const tests = [
  {
    name: "Plain numbers",
    cases: [
      { input: "1024", expected: 1024n },
      { input: "0", expected: 0n },
      { input: "999999999999999999", expected: 999999999999999999n },
    ]
  },
  {
    name: "Kilobyte values",
    cases: [
      { input: "1K", expected: 1024n },
      { input: "10K", expected: 10240n },
      { input: "1KB", expected: 1024n },
      { input: "128k", expected: 131072n }, // lowercase
    ]
  },
  {
    name: "Megabyte values",
    cases: [
      { input: "1M", expected: 1048576n },
      { input: "16M", expected: 16777216n },
      { input: "128MB", expected: 134217728n },
      { input: "1m", expected: 1048576n }, // lowercase
    ]
  },
  {
    name: "Gigabyte values",
    cases: [
      { input: "1G", expected: 1073741824n },
      { input: "2G", expected: 2147483648n },
      { input: "4GB", expected: 4294967296n },
      { input: "8g", expected: 8589934592n }, // lowercase
    ]
  },
  {
    name: "Terabyte values",
    cases: [
      { input: "1T", expected: 1099511627776n },
      { input: "2TB", expected: 2199023255552n },
      { input: "5t", expected: 5497558138880n }, // lowercase
    ]
  },
  {
    name: "Decimal values",
    cases: [
      { input: "1.5G", expected: 1610612736n },
      { input: "0.5M", expected: 524288n },
      { input: "2.25K", expected: 2304n },
      { input: "1.75T", expected: 1924145348608n },
    ]
  },
  {
    name: "Large values beyond Number.MAX_SAFE_INTEGER",
    cases: [
      { input: "10000G", expected: 10737418240000n },
      { input: "100T", expected: 109951162777600n },
      { input: "9999999999999999", expected: 9999999999999999n },
    ]
  },
  {
    name: "Values with whitespace",
    cases: [
      { input: " 1G ", expected: 1073741824n },
      { input: "  128M  ", expected: 134217728n },
      { input: " 1024 ", expected: 1024n },
    ]
  },
  {
    name: "Invalid inputs should return null",
    cases: [
      { input: "", expected: null },
      { input: null, expected: null },
      { input: undefined, expected: null },
      { input: "invalid", expected: null },
      { input: "123.45.67G", expected: null },
      { input: "1P", expected: null }, // Unsupported unit
      { input: "GB", expected: null }, // No number
      { input: "-1G", expected: null }, // Negative (if not supported)
    ]
  },
  {
    name: "Edge cases",
    cases: [
      { input: "0K", expected: 0n },
      { input: "0M", expected: 0n },
      { input: "0G", expected: 0n },
      { input: "0.0G", expected: 0n },
      { input: "1.0G", expected: 1073741824n },
    ]
  },
  {
    name: "Case insensitivity",
    cases: [
      { input: "1k", expected: 1024n },
      { input: "1K", expected: 1024n },
      { input: "1m", expected: 1048576n },
      { input: "1M", expected: 1048576n },
      { input: "1g", expected: 1073741824n },
      { input: "1G", expected: 1073741824n },
      { input: "1t", expected: 1099511627776n },
      { input: "1T", expected: 1099511627776n },
    ]
  },
  {
    name: "Precision in decimal conversions",
    cases: [
      { input: "0.1G", expected: 107374182n },
      { input: "0.01G", expected: 10737418n },
      { input: "0.001G", expected: 1073741n },
      { input: "3.14159M", expected: 3294195n }, // π megabytes (Math.floor precision)
    ]
  }
];

// Implementation of parseSizeToBytes for testing
const parseSizeToBytes = (value) => {
  if (value === null || value === undefined || value === '') return null
  
  const strValue = String(value).trim().toUpperCase()
  
  // Check if it's just a plain number
  if (/^\d+$/.test(strValue)) {
    try {
      return BigInt(strValue)
    } catch (e) {
      return null
    }
  }
  
  // Match patterns like: 4G, 128M, 1024K, 2T, 4GB, 128MB, etc.
  const match = strValue.match(/^(\d+(?:\.\d+)?)\s*([KMGT])B?$/i)
  
  if (!match) {
    return null // Not a size value
  }
  
  const [, numStr, unit] = match
  const number = parseFloat(numStr)
  
  // Use BigInt for multipliers to handle large values
  const multipliers = {
    'K': 1024n,
    'M': 1024n * 1024n,
    'G': 1024n * 1024n * 1024n,
    'T': 1024n * 1024n * 1024n * 1024n
  }
  
  const multiplier = multipliers[unit.toUpperCase()]
  if (!multiplier) return null
  
  // Handle decimal values by converting to integer first
  const integerPart = Math.floor(number)
  const decimalPart = number - integerPart
  
  try {
    let result = BigInt(integerPart) * multiplier
    
    // Add decimal portion if present (e.g., 1.5G)
    if (decimalPart > 0) {
      result += BigInt(Math.floor(decimalPart * Number(multiplier)))
    }
    
    return result
  } catch (e) {
    return null
  }
}

// Helper function to check if two values are equal considering size units
const areSizeValuesEqual = (val1, val2) => {
  const bytes1 = parseSizeToBytes(val1)
  const bytes2 = parseSizeToBytes(val2)
  
  // If either couldn't be parsed as size, fall back to string comparison
  if (bytes1 === null || bytes2 === null) {
    return String(val1) === String(val2)
  }
  
  return bytes1 === bytes2
}

// Test runner
function runTests() {
  let passed = 0;
  let failed = 0;
  const failures = [];

  tests.forEach(suite => {
    console.log(`\n Testing: ${suite.name}`);
    suite.cases.forEach(testCase => {
      const result = parseSizeToBytes(testCase.input);
      const success = result === testCase.expected;
      
      if (success) {
        passed++;
        console.log(`  ✓ ${JSON.stringify(testCase.input)} => ${result}`);
      } else {
        failed++;
        const failure = {
          suite: suite.name,
          input: testCase.input,
          expected: testCase.expected,
          actual: result
        };
        failures.push(failure);
        console.log(`  ✗ ${JSON.stringify(testCase.input)} => ${result} (expected ${testCase.expected})`);
      }
    });
  });

  console.log(`\n========================================`);
  console.log(`Total: ${passed + failed} tests`);
  console.log(`Passed: ${passed}`);
  console.log(`Failed: ${failed}`);
  
  if (failures.length > 0) {
    console.log(`\nFailures:`);
    failures.forEach(f => {
      console.log(`  ${f.suite}: ${JSON.stringify(f.input)}`);
      console.log(`    Expected: ${f.expected}`);
      console.log(`    Actual: ${f.actual}`);
    });
  }

  return failed === 0;
}

// Test areSizeValuesEqual function
function testComparison() {
  console.log('\n========================================');
  console.log('Testing areSizeValuesEqual');
  console.log('========================================');

  const comparisonTests = [
    { val1: "1024", val2: "1K", expected: true },
    { val1: "1048576", val2: "1M", expected: true },
    { val1: "1073741824", val2: "1G", expected: true },
    { val1: "2G", val2: "2048M", expected: true },
    { val1: "1G", val2: "1024M", expected: true },
    { val1: "1G", val2: "2G", expected: false },
    { val1: "1G", val2: "500M", expected: false },
    { val1: "invalid", val2: "invalid", expected: true }, // String fallback
    { val1: "test", val2: "other", expected: false },
    { val1: "1.5G", val2: "1536M", expected: true },
  ];

  let passed = 0;
  let failed = 0;

  comparisonTests.forEach(test => {
    const result = areSizeValuesEqual(test.val1, test.val2);
    const success = result === test.expected;
    
    if (success) {
      passed++;
      console.log(`  ✓ ${test.val1} == ${test.val2} => ${result}`);
    } else {
      failed++;
      console.log(`  ✗ ${test.val1} == ${test.val2} => ${result} (expected ${test.expected})`);
    }
  });

  console.log(`\nComparison tests: ${passed} passed, ${failed} failed`);
  return failed === 0;
}

// Run all tests
if (typeof module !== 'undefined' && module.exports) {
  module.exports = { parseSizeToBytes, areSizeValuesEqual, runTests, testComparison };
}

// Auto-run if executed directly
if (typeof require !== 'undefined' && require.main === module) {
  const allPassed = runTests() && testComparison();
  process.exit(allPassed ? 0 : 1);
}
